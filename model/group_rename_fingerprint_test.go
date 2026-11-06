package model

import (
	"errors"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGroupRenameFingerprintTargetsDatabaseMatrix(t *testing.T) {
	for _, dialect := range modelFingerprintTestDialects(t) {
		t.Run(string(dialect), func(t *testing.T) {
			db := setupModelFingerprintDatabase(t, dialect)
			seedGroupRenameOption(t, "GroupRatio", `{"old":1,"keep":1}`)
			owner := User{Username: "rename-fingerprint-owner", AffCode: "rename-fingerprint-owner", Group: "old", Status: common.UserStatusEnabled}
			require.NoError(t, db.Create(&owner).Error)
			config, err := UpdateModelFingerprintSiteConfig(t.Context(), 0, owner.Id, ModelFingerprintSiteConfig{
				Enabled: true, DefaultPeriodSecs: 3600, Targets: []ModelFingerprintSiteTarget{
					{Group: "old", Model: "allowed", Enabled: true, PeriodSecs: 120},
					{Group: "old", Model: "disabled", Enabled: false},
					{Group: "saved-only", Model: "retired", Enabled: false},
				},
			})
			require.NoError(t, err)
			manual := ModelTestRun{ID: uuid.NewString(), UserID: owner.Id, Source: "user", Group: "old", Model: "allowed", BenchmarkID: "local-fingerprint-v1", Status: "completed", CreatedAt: time.Now().Unix(), SamplesJSON: `[{"request_id":"historical-dispatch"}]`}
			site := ModelFingerprintSiteRun{ID: uuid.NewString(), TargetID: config.Targets[0].ID, ConfigVersion: config.Version, Window: 1, Group: "old", Model: "allowed", Status: "succeeded", CreatedAt: manual.CreatedAt}
			require.NoError(t, db.Create(&manual).Error)
			require.NoError(t, db.Create(&site).Error)
			_, err = RenameGroups([]GroupRenameMapping{{From: "old", To: "saved-only"}})
			require.ErrorIs(t, err, ErrGroupRenameCollision, "disabled targets still refer to their saved group")
			_, err = ApplyGroupRename(GroupRenameApplyRequest{ActorID: owner.Id, ActorRole: common.RoleRootUser, Mappings: []GroupRenameMapping{{From: "old", To: "new"}}})
			require.NoError(t, err)
			renamed, err := GetModelFingerprintSiteConfig(t.Context())
			require.NoError(t, err)
			assert.Equal(t, config.Version+1, renamed.Version)
			assert.Equal(t, owner.Id, renamed.UpdatedBy)
			require.Len(t, renamed.Targets, len(config.Targets))
			for i, original := range config.Targets {
				if original.Group == "old" {
					original.Group = "new"
				}
				assert.Equal(t, original, renamed.Targets[i], "identity, enabled state and scheduling are preserved")
			}
			assert.ErrorIs(t, RequireModelTestTarget(t.Context(), "old", "allowed"), ErrModelTestTargetUnavailable)
			require.NoError(t, RequireModelTestTarget(t.Context(), "new", "allowed"))
			assert.ErrorIs(t, RequireModelTestTarget(t.Context(), "new", "disabled"), ErrModelTestTargetUnavailable)
			var savedManual ModelTestRun
			var savedSite ModelFingerprintSiteRun
			require.NoError(t, db.First(&savedManual, "id = ?", manual.ID).Error)
			require.NoError(t, db.First(&savedSite, "id = ?", site.ID).Error)
			assert.Equal(t, manual, savedManual, "manual history is an immutable observation of its original scope")
			assert.Equal(t, site, savedSite, "site history retains its target, configuration version and group")
			var audits []ModelFingerprintSiteAudit
			require.NoError(t, db.Order("version").Find(&audits).Error)
			require.Len(t, audits, 2)
			assert.Contains(t, audits[0].Configuration, `"group":"old"`)
			assert.Contains(t, audits[1].Configuration, `"group":"new"`)
			assert.Equal(t, owner.Id, audits[1].ActorID)
			_, err = UpdateModelFingerprintSiteConfig(t.Context(), config.Version, owner.Id, *config)
			assert.ErrorIs(t, err, ErrFingerprintSiteConflict, "an old form must reload after a rename")
			_, err = UpdateModelFingerprintSiteConfig(t.Context(), renamed.Version, owner.Id, *config)
			assert.ErrorIs(t, err, ErrGroupReferenceRenamed, "even a fresh version cannot reintroduce a retired active group")
			previousHook := groupRenameTxFailureHook
			t.Cleanup(func() { groupRenameTxFailureHook = previousHook })
			forced := errors.New("synthetic fingerprint rename rollback")
			groupRenameTxFailureHook = func() error { return forced }
			_, err = RenameGroups([]GroupRenameMapping{{From: "new", To: "rolled-back"}})
			require.ErrorIs(t, err, forced)
			groupRenameTxFailureHook = previousHook
			afterRollback, err := GetModelFingerprintSiteConfig(t.Context())
			require.NoError(t, err)
			assert.Equal(t, renamed, afterRollback)
			var count int64
			require.NoError(t, db.Model(&ModelFingerprintSiteAudit{}).Count(&count).Error)
			assert.EqualValues(t, 2, count, "a failed rename must not append an audit")
			// Existing installations without a saved fingerprint configuration
			// still rename normally without enabling or creating this feature.
			require.NoError(t, db.Delete(&ModelFingerprintSiteConfig{}, 1).Error)
			_, err = RenameGroups([]GroupRenameMapping{{From: "new", To: "final"}})
			require.NoError(t, err)
			require.NoError(t, db.Model(&ModelFingerprintSiteConfig{}).Count(&count).Error)
			assert.Zero(t, count)
			require.NoError(t, db.Model(&ModelFingerprintSiteAudit{}).Count(&count).Error)
			assert.EqualValues(t, 2, count)
		})
	}
}
