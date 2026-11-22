package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Return an error only after the real driver has committed the status change.
// A BeforeUpdate error would not exercise the uncertain-COMMIT cache boundary.
type modelBonusCacheACKPool struct {
	*sql.DB
	committed atomic.Int32
}

type modelBonusCacheACKTx struct {
	*sql.Tx
	pool *modelBonusCacheACKPool
}

var errModelBonusCacheACK = errors.New("injected model status COMMIT acknowledgement loss")

func (p *modelBonusCacheACKPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := p.DB.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &modelBonusCacheACKTx{Tx: tx, pool: p}, nil
}

func (tx *modelBonusCacheACKTx) Commit() error {
	if err := tx.Tx.Commit(); err != nil {
		return err
	}
	tx.pool.committed.Add(1)
	return errModelBonusCacheACK
}

func TestModelBonusCacheStatusOnlyCommitACKLoss(t *testing.T) {
	// This focused handler test owns an isolated SQLite database. It is not
	// evidence of native row locking or a replacement for the native matrix.
	db := modelManagementDB(t, "sqlite", "")
	model.InvalidatePricingCache()
	model.InvalidateModelBonusEligibleCache()
	t.Cleanup(func() {
		model.InvalidatePricingCache()
		model.InvalidateModelBonusEligibleCache()
	})

	const child = "status-ack-rule-child"
	rule := model.Model{ModelName: "status-ack-rule-", NameRule: model.NameRulePrefix, Status: 1, SyncOfficial: 1, BonusEligible: boolPtr(true)}
	require.NoError(t, rule.Insert())
	// The existing exact-name fallback deliberately does not require catalog
	// visibility. This fix must revoke only the stale rule-derived grant.
	exact := model.Model{ModelName: "status-ack-exact-control", Status: 0, SyncOfficial: 1, BonusEligible: boolPtr(true)}
	require.NoError(t, exact.Insert())
	channel := model.Channel{Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Name: "status-ack-test-channel", Key: "synthetic-status-ack", Models: child, Group: "default"}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, db.Create(&model.Ability{Group: "default", Model: child, ChannelId: channel.Id, Enabled: true}).Error)
	var exactChildRows int64
	require.NoError(t, db.Model(&model.Model{}).Where("model_name = ?", child).Count(&exactChildRows).Error)
	require.Zero(t, exactChildRows, "the child must receive eligibility from the pricing rule, not exact-name fallback")
	require.True(t, model.IsModelBonusEligible(child), "warm both positive caches through the real prefix rule")
	require.True(t, model.IsModelBonusEligible(exact.ModelName), "preserve the pre-existing exact-name fallback")

	wrapped := db.Session(&gorm.Session{NewDB: true, Context: context.Background()})
	config := *wrapped.Config
	wrapped.Config = &config
	// The production fixture enables prepared statements, so db.ConnPool may
	// be *gorm.PreparedStmtDB rather than a gorm.TxBeginner. Inject at the
	// actual driver pool without weakening that production configuration.
	sqlDB, err := db.DB()
	require.NoError(t, err)
	pool := &modelBonusCacheACKPool{DB: sqlDB}
	wrapped.Config.ConnPool, wrapped.Statement.ConnPool = pool, pool
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("role", common.RoleRootUser)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/models/?status_only=true", strings.NewReader(fmt.Sprintf(`{"id":%d,"status":0}`, rule.Id)))
	c.Request.Header.Set("Content-Type", "application/json")
	func() {
		model.DB = wrapped
		defer func() { model.DB = db }()
		UpdateModelMeta(c)
	}()
	require.EqualValues(t, 1, pool.committed.Load(), "the production handler must reach exactly one real successful COMMIT before losing its acknowledgement")
	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.False(t, response.Success)
	require.Contains(t, response.Message, errModelBonusCacheACK.Error(), "reject an unrelated binding or SQL failure as false RED evidence")
	var stored model.Model
	require.NoError(t, db.First(&stored, rule.Id).Error)
	require.Zero(t, stored.Status, "the database change actually committed")
	require.Equal(t, model.NameRulePrefix, stored.NameRule)
	require.Equal(t, rule.ModelName, stored.ModelName)
	require.NotNil(t, stored.BonusEligible)
	require.True(t, *stored.BonusEligible, "status_only must not erase the underlying bonus policy")
	assert.False(t, model.IsModelBonusEligible(child), "an acknowledged error must not keep a rule-derived positive cache after its status revocation really committed")
	var storedExact model.Model
	require.NoError(t, db.First(&storedExact, exact.Id).Error)
	assert.Zero(t, storedExact.Status)
	assert.True(t, model.IsModelBonusEligible(exact.ModelName), "do not change existing exact-name eligibility semantics")
}
