package service

import (
	"context"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestLaneStoreBindingDraftStaysDisabledPostgres(t *testing.T) {
	raw := os.Getenv("TEST_SERVICE_POSTGRES_DSN")
	if raw == "" {
		t.Skip("requires disposable loopback PostgreSQL TEST_SERVICE_POSTGRES_DSN")
	}
	dsn, err := url.Parse(raw)
	require.NoError(t, err)
	addr, err := netip.ParseAddr(dsn.Hostname())
	require.NoError(t, err)
	require.True(t, addr.IsLoopback())
	t.Setenv(model.UserGroupPricingEnabledEnv, "false")
	schema := "lane_store_service_" + common.GetUUID()[:8]
	require.Regexp(t, `^lane_store_service_[a-f0-9]{8}$`, schema)
	require.NoError(t, model.DB.Exec(fmt.Sprintf(`CREATE SCHEMA %q`, schema)).Error)
	t.Cleanup(func() { require.NoError(t, model.DB.Exec(fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schema)).Error) })
	q := dsn.Query()
	q.Set("search_path", schema)
	dsn.RawQuery = q.Encode()
	db, err := gorm.Open(postgres.Open(dsn.String()), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.Option{}, &model.BonusSourceState{}, &model.BillingFinalization{}))
	release := model.LaneStoreReleaseV1{StoreID: strings.Repeat("a", 32), ReleaseID: strings.Repeat("b", 32),
		BinarySHA256: strings.Repeat("c", 64), Epoch: 1}
	require.NoError(t, model.InstallLaneStoreAuthorityV1(t.Context(), db, release))
	_, info, sealed, _ := setupNativeChatSettlement(t)
	// The fixture has a fresh group namespace revision of zero, not the
	// numeric test-only sentinel used by the existing settlement fixture.
	price, err := PrepareLaneNativeChatPrice(info, 0)
	require.NoError(t, err)
	route := LaneNativeChatRoute{ChannelID: info.ChannelId, ChannelType: info.ChannelType, Group: info.UsingGroup,
		Model: info.OriginModelName, Path: "/v1/chat/completions", AuthoritySHA256: strings.Repeat("a", 64)}
	key := [32]byte{99}
	proof, err := price.VerifyFinalWire(t.Context(), route, sealed.body, key, laneSettlementPromptBound{})
	require.NoError(t, err)
	const testKey = "synthetics8bindingsvcfinite"
	user := model.User{Id: 601, Username: "synthetic-s8-svc", AffCode: "synth-s8-svc", Status: common.UserStatusEnabled,
		Group: info.UsingGroup, Quota: 100_000, QuotaPaid: 100_000, AuthVersion: 2, CacheRevision: 3}
	user.SetSetting(dto.UserSetting{BillingPreference: "wallet_only"})
	token := model.Token{Id: 602, UserId: user.Id, Key: testKey, Status: common.TokenStatusEnabled,
		Group: info.UsingGroup, RemainQuota: 100_000, ExpiredTime: -1, CacheRevision: 5}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&token).Error)
	in := model.LaneStoreQuoteInputV1{Release: release, RequestID: info.RequestId, UserID: user.Id, TokenID: token.Id,
		TokenFingerprint: model.BillingTokenFingerprint(testKey), UserAuthVersion: user.AuthVersion,
		UserCacheRevision: user.CacheRevision, TokenCacheRevision: token.CacheRevision, Group: info.UsingGroup}
	inTx := func(fn func(*gorm.DB) error) error {
		ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
		defer cancel()
		return db.WithContext(ctx).Transaction(fn)
	}
	var facts model.LaneStoreQuoteAuthorityV1
	require.NoError(t, inTx(func(tx *gorm.DB) error {
		facts, err = model.CaptureLaneStoreQuoteAuthorityV1Tx(tx, in)
		return err
	}))
	draft, err := PrepareLaneStoreBindingDraftV1(price, facts)
	require.NoError(t, err)
	evaluate := func(p LaneNativeChatProof) (LaneStoreBindingDecisionV1, error) {
		var out LaneStoreBindingDecisionV1
		err := inTx(func(tx *gorm.DB) error {
			var err error
			out, err = VerifyLaneStoreFinalBindingDraftV1Tx(tx, draft, p)
			return err
		})
		return out, err
	}
	got, err := evaluate(proof)
	require.NoError(t, err)
	assert.True(t, got.DefaultEligible)
	assert.False(t, got.CanBind)
	got, err = evaluate(LaneNativeChatProof{evidence: proof.Evidence()})
	require.ErrorIs(t, err, model.ErrLaneStoreAuthorityEvidence)
	assert.Zero(t, got)
	changedPrice := price
	changedPrice.quote.ModelRatio *= 2
	mismatched, err := changedPrice.VerifyFinalWire(t.Context(), route, sealed.body, key, laneSettlementPromptBound{})
	require.NoError(t, err)
	_, err = evaluate(mismatched)
	require.ErrorIs(t, err, model.ErrLaneStoreAuthorityEvidence)
	require.NoError(t, db.Model(&model.Token{}).Where("id = ?", token.Id).UpdateColumn("status", common.TokenStatusDisabled).Error)
	got, err = evaluate(proof)
	require.ErrorIs(t, err, model.ErrLaneStoreAuthorityEvidence)
	assert.Zero(t, got)
	var owners int64
	require.NoError(t, db.Model(&model.BillingFinalization{}).Count(&owners).Error)
	assert.Zero(t, owners)
	var reloaded model.User
	require.NoError(t, db.Take(&reloaded, user.Id).Error)
	assert.Equal(t, user.QuotaPaid, reloaded.QuotaPaid)
}
