package model

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSyncTopicMigrationAndCommitOrderDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := useSystemTaskMatrixDB(t, dialect)
			// This is the released sync schema: task history exists, but the
			// permanent topic lock table has not been introduced yet.
			require.False(t, db.Migrator().HasTable(&SystemSyncTopic{}))
			legacy := SystemTask{TaskID: "sync-before-upgrade", Type: SyncTaskType("upgraded"), Status: SystemTaskStatusSucceeded, Payload: `{"node":"old-node"}`}
			require.NoError(t, db.Create(&legacy).Error)
			tables := []any{&SystemSyncTopic{}, &Option{}}
			t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(tables...)) })
			for range 2 {
				require.NoError(t, db.AutoMigrate(&SystemTask{}, &SystemSyncTopic{}, &Option{}))
			}
			var stored SystemTask
			require.NoError(t, db.First(&stored, legacy.ID).Error)
			assert.Equal(t, legacy, stored, "upgrade and restart preserve the released task history")
			duplicate := SystemTask{TaskID: legacy.TaskID, Type: "duplicate", Status: SystemTaskStatusPending}
			require.Error(t, db.Create(&duplicate).Error, "task identity uniqueness survives the upgrade")
			BumpSyncTopic("upgraded")
			revisions, err := GetLatestSyncRevisions()
			require.NoError(t, err)
			assert.Greater(t, revisions["upgraded"], legacy.ID)

			sqlDB, err := db.DB()
			require.NoError(t, err)
			if dialect != "sqlite" {
				sqlDB.SetMaxOpenConns(4)
			}
			for _, topic := range []string{"first-write", "upgraded"} {
				t.Run(topic, func(t *testing.T) {
					before, err := GetLatestSyncRevisions()
					require.NoError(t, err)
					first := db.Begin()
					require.NoError(t, first.Error)
					t.Cleanup(func() { _ = first.Rollback().Error })
					require.NoError(t, first.Create(&Option{Key: topic + "-first", Value: "committed first"}).Error)
					require.NoError(t, bumpSyncTopic(first, topic))
					var firstTask SystemTask
					require.NoError(t, first.Where("type = ?", SyncTaskType(topic)).Order("id desc").First(&firstTask).Error)

					if dialect != "sqlite" {
						// A later independent writer must not commit a higher ID
						// while this write is still invisible. Its deadline cancels
						// the wait; no sleeps or scheduler ordering are assumed.
						ctx, cancel := context.WithTimeout(t.Context(), time.Second)
						err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
							if err := tx.Create(&Option{Key: topic + "-second", Value: "committed second"}).Error; err != nil {
								return err
							}
							return bumpSyncTopic(tx, topic)
						})
						require.Error(t, err, "a newer revision cannot commit before the preceding topic write")
						assert.ErrorIs(t, ctx.Err(), context.DeadlineExceeded, "the competing transaction must wait for the topic lock")
						cancel()
						visible, err := GetLatestSyncRevisions()
						require.NoError(t, err)
						assert.Equal(t, before[topic], visible[topic])
					}
					require.NoError(t, first.Commit().Error)
					firstVisible, err := GetLatestSyncRevisions()
					require.NoError(t, err)
					assert.Equal(t, firstTask.ID, firstVisible[topic])

					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						if err := tx.Create(&Option{Key: topic + "-second", Value: "committed second"}).Error; err != nil {
							return err
						}
						return bumpSyncTopic(tx, topic)
					}))
					latest, err := GetLatestSyncRevisions()
					require.NoError(t, err)
					assert.Greater(t, latest[topic], firstVisible[topic], "the next commit must cause a follower reload")
					var values []string
					require.NoError(t, db.Model(&Option{}).Where(map[string]any{"key": []string{topic + "-first", topic + "-second"}}).Pluck("value", &values).Error)
					assert.ElementsMatch(t, []string{"committed first", "committed second"}, values)
				})
			}

			before, err := GetLatestSyncRevisions()
			require.NoError(t, err)
			rollback := errors.New("rollback topic write")
			err = db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Create(&Option{Key: "rolled-back", Value: "never visible"}).Error; err != nil {
					return err
				}
				if err := bumpSyncTopic(tx, "upgraded"); err != nil {
					return err
				}
				return rollback
			})
			require.ErrorIs(t, err, rollback)
			after, err := GetLatestSyncRevisions()
			require.NoError(t, err)
			assert.Equal(t, before, after, "a rolled-back write publishes no revision")
			var count int64
			require.NoError(t, db.Model(&Option{}).Where(map[string]any{"key": "rolled-back"}).Count(&count).Error)
			assert.Zero(t, count)
		})
	}
}
