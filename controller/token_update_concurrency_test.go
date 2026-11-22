package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTokenWriteRegression(t *testing.T) (*gorm.DB, *model.User, *model.Token) {
	t.Helper()
	if os.Getenv("TEST_TASK_DB_DIALECT") != "postgres" {
		t.Skip("requires the isolated production PostgreSQL fixture")
	}
	configureTokenAutoGroupsTest(t, "5", `["default","vip"]`)
	user := setupTokenAutoGroupsControllerTest(t)
	token := seedToken(t, model.DB, user.Id, "before", "synthetic-token-write-regression")
	token.UnlimitedQuota = false
	require.NoError(t, model.DB.Save(token).Error)
	return model.DB, user, token
}

// Commit a second operation after the controller has read its snapshot, before
// it starts its write transaction. No sleeps or scheduling assumptions are used.
func commitAfterTokenRead(t *testing.T, db *gorm.DB, commit func(*model.Token)) *bool {
	t.Helper()
	fired := new(bool)
	const callback = "token_write_regression_after_read"
	require.NoError(t, db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		current, ok := tx.Statement.Dest.(*model.Token)
		if *fired || !ok || current.Id == 0 || tx.Error != nil {
			return
		}
		*fired = true
		snapshot := *current
		commit(&snapshot)
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove(callback)) })
	return fired
}

func TestTokenStatusOnlyPreservesConcurrentQuotaAndRouting(t *testing.T) {
	db, user, token := setupTokenWriteRegression(t)
	fired := commitAfterTokenRead(t, db, func(current *model.Token) {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			return model.ApplyTokenQuotaDeltaTx(tx, current.Id, current.Key, -20)
		}))
		// A separate editor narrows the key while the status toggle holds an old
		// snapshot. Its committed settings must survive the toggle.
		current.RemainQuota, current.UsedQuota = 80, 20
		current.Group, current.ModelLimitsEnabled, current.ModelLimits = "vip", true, "allowed-model"
		require.NoError(t, current.Update())
	})
	ctx, recorder := newTokenAutoGroupsAuthenticatedContext(t, http.MethodPut, "/api/token/?status_only=true", map[string]any{
		"id": token.Id, "status": common.TokenStatusDisabled,
		"routing_strategy": "invalid",
	}, user.Id)
	UpdateToken(ctx)
	response := decodeAPIResponse(t, recorder)
	require.True(t, *fired)
	require.True(t, response.Success, response.Message)
	var saved model.Token
	require.NoError(t, db.First(&saved, token.Id).Error)
	assert.Equal(t, common.TokenStatusDisabled, saved.Status)
	assert.Equal(t, 80, saved.RemainQuota)
	assert.Equal(t, 20, saved.UsedQuota)
	assert.Equal(t, "vip", saved.Group)
	assert.True(t, saved.ModelLimitsEnabled)
	assert.Equal(t, "allowed-model", saved.ModelLimits)
	assert.Equal(t, "before", saved.Name)
	assert.NotContains(t, recorder.Body.String(), token.Key)
}

func TestTokenStatusOnlyChecksLatestEligibility(t *testing.T) {
	for _, tc := range []struct {
		name, group string
		patch       map[string]any
		message     string
		want        bool
	}{
		{name: "quota consumed", patch: map[string]any{"remain_quota": 0, "used_quota": 100}, message: "Token quota is exhausted"},
		{name: "expiry changed", patch: map[string]any{"expired_time": 1}, message: "Token has expired"},
		{name: "routing corrected", group: "retired", patch: map[string]any{"group": "vip"}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, user, token := setupTokenWriteRegression(t)
			require.NoError(t, db.Model(token).Update("status", common.TokenStatusDisabled).Error)
			if tc.group != "" {
				require.NoError(t, db.Model(token).Update("group", tc.group).Error)
			}
			fired := commitAfterTokenRead(t, db, func(current *model.Token) {
				require.NoError(t, db.Model(current).Updates(tc.patch).Error)
			})
			ctx, recorder := newTokenAutoGroupsAuthenticatedContext(t, http.MethodPut, "/api/token/?status_only=true", map[string]any{
				"id": token.Id, "status": common.TokenStatusEnabled,
			}, user.Id)
			UpdateToken(ctx)
			response := decodeAPIResponse(t, recorder)
			require.True(t, *fired)
			assert.Equal(t, tc.want, response.Success, response.Message)
			var saved model.Token
			require.NoError(t, db.First(&saved, token.Id).Error)
			if tc.want {
				assert.Equal(t, common.TokenStatusEnabled, saved.Status)
				assert.Equal(t, "vip", saved.Group)
			} else {
				assert.Contains(t, response.Message, tc.message)
				assert.Equal(t, common.TokenStatusDisabled, saved.Status)
			}
		})
	}
}

func TestTokenUpdateKeepsConsumptionAndSupportsOmittedQuota(t *testing.T) {
	for _, tc := range []struct {
		name    string
		quota   *int
		consume int
		want    int
	}{
		{"omitted", nil, 20, 80},
		{"explicit adjustment", common.GetPointer(200), 20, 180},
		{"explicit zero", common.GetPointer(0), 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, user, token := setupTokenWriteRegression(t)
			fired := commitAfterTokenRead(t, db, func(current *model.Token) {
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					return model.ApplyTokenQuotaDeltaTx(tx, current.Id, current.Key, -tc.consume)
				}))
			})
			request := baseTokenRequest("after")
			request["id"], request["group"], request["unlimited_quota"] = token.Id, "vip", false
			delete(request, "remain_quota")
			if tc.quota != nil {
				request["remain_quota"] = *tc.quota
			}
			ctx, recorder := newTokenAutoGroupsAuthenticatedContext(t, http.MethodPut, "/api/token/", request, user.Id)
			UpdateToken(ctx)
			response := decodeAPIResponse(t, recorder)
			require.True(t, *fired)
			require.True(t, response.Success, response.Message)
			var saved, returned model.Token
			require.NoError(t, db.First(&saved, token.Id).Error)
			require.NoError(t, common.Unmarshal(response.Data, &returned))
			assert.Equal(t, tc.want, saved.RemainQuota)
			assert.Equal(t, tc.consume, saved.UsedQuota)
			assert.Equal(t, saved.RemainQuota, returned.RemainQuota)
			assert.Equal(t, saved.UsedQuota, returned.UsedQuota)
			assert.Equal(t, "after", saved.Name)
			assert.Equal(t, "vip", saved.Group)
		})
	}
}

func TestTokenUpdateRejectsConcurrentRevocation(t *testing.T) {
	db, user, token := setupTokenWriteRegression(t)
	fired := commitAfterTokenRead(t, db, func(current *model.Token) {
		current.Status = common.TokenStatusDisabled
		current.ModelLimitsEnabled, current.ModelLimits = true, "allowed-model"
		require.NoError(t, current.Update())
	})
	request := baseTokenRequest("stale edit")
	request["id"], request["group"] = token.Id, "vip"
	ctx, recorder := newTokenAutoGroupsAuthenticatedContext(t, http.MethodPut, "/api/token/", request, user.Id)
	UpdateToken(ctx)
	response := decodeAPIResponse(t, recorder)
	require.True(t, *fired)
	assert.False(t, response.Success)
	assert.Equal(t, "Token settings changed; reload and try again", response.Message)
	var saved model.Token
	require.NoError(t, db.First(&saved, token.Id).Error)
	assert.Equal(t, common.TokenStatusDisabled, saved.Status)
	assert.Equal(t, "before", saved.Name)
	assert.Equal(t, "default", saved.Group)
	assert.True(t, saved.ModelLimitsEnabled)
	assert.Equal(t, "allowed-model", saved.ModelLimits)
}

func TestTokenNameLimitCountsUnicodeCharacters(t *testing.T) {
	db, user, token := setupTokenWriteRegression(t)
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		for _, count := range []int{50, 51} {
			request := baseTokenRequest(strings.Repeat("密", count))
			if method == http.MethodPut {
				request["id"] = token.Id
			}
			ctx, recorder := newTokenAutoGroupsAuthenticatedContext(t, method, "/api/token/", request, user.Id)
			if method == http.MethodPost {
				AddToken(ctx)
			} else {
				UpdateToken(ctx)
			}
			response := decodeAPIResponse(t, recorder)
			assert.Equal(t, count == 50, response.Success, "%s name length %d: %s", method, count, response.Message)
		}
	}
	var saved model.Token
	require.NoError(t, db.First(&saved, token.Id).Error)
	assert.Equal(t, strings.Repeat("密", 50), saved.Name)
}

func TestTokenCreateEnforcesLimitAfterConcurrentCreation(t *testing.T) {
	db, user, _ := setupTokenWriteRegression(t)
	settings := operation_setting.GetTokenSetting()
	previousLimit := settings.MaxUserTokens
	settings.MaxUserTokens = 2
	t.Cleanup(func() { settings.MaxUserTokens = previousLimit })
	const callback = "token_create_regression_after_count"
	fired := false
	require.NoError(t, db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if fired || tx.Statement.Table != "tokens" || tx.Error != nil {
			return
		}
		if _, ok := tx.Statement.Dest.(*int64); !ok {
			return
		}
		fired = true
		other := model.Token{
			UserId: user.Id, Name: "second slot", Key: "synthetic-concurrent-token-slot",
			Group: "default", ExpiredTime: -1, RemainQuota: 100,
		}
		require.NoError(t, other.Insert(user.AuthVersion))
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove(callback)) })
	ctx, recorder := newTokenAutoGroupsAuthenticatedContext(t, http.MethodPost, "/api/token/", baseTokenRequest("third slot"), user.Id)
	AddToken(ctx)
	response := decodeAPIResponse(t, recorder)
	require.True(t, fired)
	assert.False(t, response.Success)
	assert.Equal(t, "Maximum number of tokens reached (2)", response.Message)
	count, err := model.CountUserTokens(user.Id)
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)
}

func TestKeyEditorSnapshotAndPartialUpdate(t *testing.T) {
	db, user, token := setupTokenWriteRegression(t)
	token.AllowIps = common.GetPointer("192.0.2.4")
	token.ModelLimitsEnabled, token.ModelLimits = true, "restricted"
	token.ExpiredTime = common.GetTimestamp() + 86400
	require.NoError(t, db.Save(token).Error)
	version := token.CacheRevision
	update := func(payload map[string]any) (*model.Token, int, bool) {
		t.Helper()
		payload["id"] = token.Id
		ctx, recorder := newTokenAutoGroupsAuthenticatedContext(t, http.MethodPut, "/api/token/", payload, user.Id)
		UpdateToken(ctx)
		res := decodeAPIResponse(t, recorder)
		var saved model.Token
		require.NoError(t, db.First(&saved, token.Id).Error)
		if res.Success {
			var returned tokenResponse
			require.NoError(t, common.Unmarshal(res.Data, &returned))
			assert.Equal(t, saved.CacheRevision, returned.SettingsVersion)
		}
		return &saved, recorder.Code, res.Success
	}
	saved, _, ok := update(map[string]any{"expected_version": version, "project_label": "Synthetic project"})
	require.True(t, ok)
	assert.Equal(t, token.Name, saved.Name)
	assert.Equal(t, token.ExpiredTime, saved.ExpiredTime)
	assert.Equal(t, token.ModelLimits, saved.ModelLimits)
	assert.True(t, saved.ModelLimitsEnabled)
	assert.Equal(t, token.AllowIps, saved.AllowIps)
	assert.Greater(t, saved.CacheRevision, version)
	_, status, ok := update(map[string]any{"expected_version": version, "name": "old edit"})
	assert.False(t, ok)
	assert.Equal(t, http.StatusConflict, status)
}

func TestKeyExplicitBudgetTransitionAndExpiry(t *testing.T) {
	db, user, token := setupTokenWriteRegression(t)
	require.NoError(t, db.Model(token).Updates(map[string]any{"unlimited_quota": true, "remain_quota": -20}).Error)
	for _, fields := range []map[string]any{{"unlimited_quota": false}, {"expired_time": -2}, {"expired_time": 0}} {
		fields["id"] = token.Id
		ctx, rr := newTokenAutoGroupsAuthenticatedContext(t, http.MethodPut, "/api/token/", fields, user.Id)
		UpdateToken(ctx)
		require.False(t, decodeAPIResponse(t, rr).Success)
	}
	ctx, rr := newTokenAutoGroupsAuthenticatedContext(t, http.MethodPut, "/api/token/", map[string]any{"id": token.Id, "unlimited_quota": false, "remain_quota": 0}, user.Id)
	UpdateToken(ctx)
	require.True(t, decodeAPIResponse(t, rr).Success)
}

func TestKeyListEffectiveStatusAndExactCredential(t *testing.T) {
	db, user, token := setupTokenWriteRegression(t)
	require.NoError(t, db.Model(token).Updates(map[string]any{"expired_time": 1, "remain_quota": 0}).Error)
	for _, state := range []int{common.TokenStatusEnabled, common.TokenStatusExpired, common.TokenStatusExhausted} {
		rows, err := model.GetAllUserTokens(user.Id, 0, 20, state)
		require.NoError(t, err)
		if state == common.TokenStatusExpired {
			require.Len(t, rows, 1)
			assert.Equal(t, state, buildMaskedTokenResponse(rows[0]).EffectiveStatus)
		} else {
			assert.Empty(t, rows)
		}
	}
	for _, tc := range []struct {
		key  string
		want int64
	}{{token.Key, 1}, {"synthetic%", 0}} {
		_, count, err := model.SearchUserTokens(user.Id, "", tc.key, 0, 20)
		require.NoError(t, err)
		assert.Equal(t, tc.want, count)
	}
}

func TestKeyEmergencyRevocationIndependentOfRoutingConfiguration(t *testing.T) {
	db, user, token := setupTokenWriteRegression(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.UserAccessToken{}, &model.AuditLog{}))
	require.NoError(t, db.Save(&model.Option{Key: "GroupConfigVersion", Value: "temporarily-unavailable"}).Error)
	register := func(raw, scope string) {
		pat := model.UserAccessToken{UserId: user.Id, Name: "key-operation", TokenHash: model.AccessTokenFingerprint(raw), ExpiresAt: time.Now().Add(time.Hour).Unix()}
		require.NoError(t, pat.SetScopes([]string{scope}))
		require.NoError(t, db.Create(&pat).Error)
	}
	writer, reader := model.AccessTokenPrefix+"synthetic-key-write", model.AccessTokenPrefix+"synthetic-key-read"
	register(writer, "api_key:write")
	register(reader, "api_key:read")
	r := gin.New()
	r.PUT("/api/token/", middleware.TokenManagementAuth(), middleware.TokenOperationAudit(), UpdateToken)
	change := func(auth, suffix string, status int) (int, bool) {
		raw, err := common.Marshal(map[string]any{"id": token.Id, "status": status})
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPut, "/api/token/?status_only=true"+suffix, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+auth)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code, decodeAPIResponse(t, rec).Success
	}
	code, ok := change(reader, "&disable_only=true", common.TokenStatusDisabled)
	assert.Equal(t, http.StatusForbidden, code)
	assert.False(t, ok)
	code, ok = change(writer, "&disable_only=true", common.TokenStatusDisabled)
	assert.Equal(t, http.StatusOK, code)
	assert.True(t, ok)
	_, ok = change(writer, "&disable_only=true", common.TokenStatusEnabled)
	assert.False(t, ok)
	code, ok = change(writer, "", common.TokenStatusEnabled)
	assert.Equal(t, http.StatusServiceUnavailable, code)
	assert.False(t, ok)
	var saved model.Token
	require.NoError(t, db.First(&saved, token.Id).Error)
	assert.Equal(t, common.TokenStatusDisabled, saved.Status)
}
