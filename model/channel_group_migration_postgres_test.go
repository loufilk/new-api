package model

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestChannelGroupListPostgres(t *testing.T) {
	if !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		t.Skip("requires a disposable TEST_MODEL_POSTGRES_DSN")
	}

	for _, legacy := range []bool{false, true} {
		name := "fresh"
		if legacy {
			name = "legacy"
		}
		t.Run(name, func(t *testing.T) {
			tx := DB.Begin()
			require.NoError(t, tx.Error)
			defer tx.Rollback()
			require.NoError(t, tx.Exec("CREATE SCHEMA channel_group_list_fixture").Error)
			require.NoError(t, tx.Exec("SET LOCAL search_path TO channel_group_list_fixture").Error)

			if legacy {
				require.NoError(t, tx.Exec(`CREATE TABLE channels (id bigserial PRIMARY KEY, "key" text NOT NULL, "group" varchar(64) DEFAULT 'default')`).Error)
				require.NoError(t, tx.Exec(`INSERT INTO channels(id, "key", "group") VALUES (-1, 'synthetic-fixture', 'default,原有分组')`).Error)
			}
			require.NoError(t, migrateChannelGroupToText(tx))
			require.NoError(t, tx.AutoMigrate(&Channel{}, &Ability{}, &Option{}))

			if legacy {
				var old Channel
				require.NoError(t, tx.First(&old, -1).Error)
				require.Equal(t, "default,原有分组", string(old.Group))
			}

			ch := Channel{Name: "group-list-fixture", Key: "synthetic-fixture", Models: "fixture-a,fixture-b"}
			require.NoError(t, tx.Create(&ch).Error)
			require.Equal(t, "default", string(ch.Group))
			require.NoError(t, ch.AddAbilities(tx))
			var defaultGroup string
			require.NoError(t, tx.Raw(`INSERT INTO channels("key") VALUES ('synthetic-default') RETURNING "group"`).Scan(&defaultGroup).Error)
			require.Equal(t, "default", defaultGroup)

			groups := []string{"普通模型专用测试分组一", "企业高并发专用测试分组二", "企业低延迟专用测试分组三", "稳定折扣专用测试分组四", "zhiyuan-专用测试分组五", "专用测试分组六"}
			require.Greater(t, len([]rune(strings.Join(groups, ","))), 64)
			ch.Group = ChannelGroupList(strings.Join(groups, ","))
			require.NoError(t, tx.Model(&ch).Update("group", ch.Group).Error)
			require.NoError(t, ch.UpdateAbilities(tx))

			// A later master startup must not narrow the field or truncate a
			// list that was written after upgrade.
			require.NoError(t, migrateChannelGroupToText(tx))
			require.NoError(t, tx.AutoMigrate(&Channel{}, &Ability{}))
			var stored Channel
			require.NoError(t, tx.First(&stored, ch.Id).Error)
			require.Equal(t, ch.Group, stored.Group)
			require.Equal(t, groups, stored.GetGroups())
			var routes []Ability
			require.NoError(t, tx.Where("channel_id = ?", ch.Id).Find(&routes).Error)
			require.Len(t, routes, len(groups)*2)
			for _, group := range groups {
				for _, modelName := range []string{"fixture-a", "fixture-b"} {
					require.Contains(t, routes, Ability{Group: group, Model: modelName, ChannelId: ch.Id, Enabled: true, Priority: ch.Priority})
				}
			}

			var typeNames []string
			require.NoError(t, tx.Raw(`SELECT format_type(atttypid, atttypmod) FROM pg_attribute
				WHERE attrelid IN ('channels'::regclass, 'abilities'::regclass) AND attname = 'group' ORDER BY 1`).Scan(&typeNames).Error)
			require.Equal(t, []string{"character varying(64)", "text"}, typeNames)
		})
	}
}
