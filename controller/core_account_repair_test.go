package controller

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCoreAccountPasswordRecoveryContracts(t *testing.T) {
	user, _ := setupSecurityEnrollmentTest(t)
	email := "fixture+test@example.invalid"
	require.NoError(t, model.DB.Model(user).Update("email", email).Error)
	_, err := model.IssuePasswordRecovery(t.Context(), email, time.Minute)
	require.ErrorIs(t, err, model.ErrAuthFlowInvalid)
	require.NoError(t, model.DB.Model(user).Updates(map[string]any{
		"email_verified_address": email, "email_verification_version": 1, "email_verified_at": time.Now().Unix(),
	}).Error)
	stale, err := model.IssuePasswordRecovery(t.Context(), email, time.Minute)
	require.NoError(t, err)
	fresh, err := model.IssuePasswordRecovery(t.Context(), email, time.Minute)
	require.NoError(t, err)
	_, err = model.FinishPasswordRecovery(email, stale, "Fixture-password-12")
	require.ErrorIs(t, err, model.ErrAuthFlowInvalid)
	link, err := passwordRecoveryURL("https://example.invalid/console", email, fresh)
	require.NoError(t, err)
	parsed, err := url.Parse(link)
	require.NoError(t, err)
	assert.Equal(t, email, parsed.Query().Get("email"))
	assert.Equal(t, "/console/user/reset", parsed.Path)
	assert.True(t, fresh == parsed.Query().Get("token"))

	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			_, err := model.FinishPasswordRecovery(email, fresh, "Fixture-password-12")
			results <- err
		})
	}
	wg.Wait()
	close(results)
	var successes int
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		assert.ErrorIs(t, err, model.ErrAuthFlowConsumed)
	}
	require.Equal(t, 1, successes)
	stored, err := model.GetUserById(user.Id, true)
	require.NoError(t, err)
	assert.EqualValues(t, user.AuthVersion+1, stored.AuthVersion)
	assert.True(t, common.ValidatePasswordAndHash("Fixture-password-12", stored.Password))

	next, err := model.IssuePasswordRecovery(t.Context(), email, time.Minute)
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(user).Update("email_verification_version", 2).Error)
	_, err = model.FinishPasswordRecovery(email, next, "Other-fixture-password-12")
	require.ErrorIs(t, err, model.ErrAuthFlowInvalid)
}

func TestCoreAccountAdminWriteRechecksAuthority(t *testing.T) {
	actor, identity, target := setupAdminUserTest(t)
	require.NoError(t, model.DB.Transaction(func(tx *gorm.DB) error {
		return model.ValidateAdminUserEditTx(tx, identity, *target)
	}))
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", target.Id).Update("role", common.RoleAdminUser).Error)
	require.Error(t, model.DB.Transaction(func(tx *gorm.DB) error {
		return model.ValidateAdminUserEditTx(tx, identity, *target)
	}))
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", target.Id).Update("role", common.RoleCommonUser).Error)
	_, err := model.RevokeUserSession(actor.Id, identity.SessionID, "fixture_revoked")
	require.NoError(t, err)
	require.ErrorIs(t, model.DB.Transaction(func(tx *gorm.DB) error {
		return model.ValidateAdminUserEditTx(tx, identity, *target)
	}), model.ErrUserSessionInactive)
	require.ErrorIs(t, model.ClearUserBindingByAdministrator(identity, *target, "wechat"), model.ErrUserSessionInactive)
	require.ErrorIs(t, model.DeleteUserOAuthBindingByAdministrator(identity, *target, 1), model.ErrUserSessionInactive)
	require.ErrorIs(t, model.DeletePasskeyByAdministrator(identity, *target), model.ErrUserSessionInactive)
	require.ErrorIs(t, model.DisableTwoFAByAdministrator(identity, *target), model.ErrUserSessionInactive)
}

func TestCoreAccountNotificationSettingsAreWriteOnly(t *testing.T) {
	root, identity := setupSecurityEnrollmentTest(t)
	require.NoError(t, model.DB.Model(root).Update("role", common.RoleRootUser).Error)
	user := model.User{Username: "notification-fixture", Role: common.RoleCommonUser, AffCode: "fixture-notify"}
	const hook, secret = "https://example.invalid/fixture-destination", "fixture-webhook-secret"
	user.SetSetting(dto.UserSetting{
		NotifyType: dto.NotifyTypeWebhook, WebhookUrl: hook, WebhookSecret: secret,
		BarkUrl: "https://example.invalid/fixture-bark", GotifyUrl: "https://example.invalid/fixture-gotify", GotifyToken: "fixture-gotify-token",
	})
	require.NoError(t, model.DB.Create(&user).Error)
	self := adminUserRequest(http.MethodGet, "/api/user/self", "", "", service.AuthIdentity{UserID: user.Id}, common.RoleCommonUser, nil, GetSelf)
	assert.Contains(t, self.Body.String(), "notification_destination_configured")
	for _, test := range []struct {
		p string
		h gin.HandlerFunc
	}{
		{"/api/user/", GetAllUsers},
		{"/api/user/search?keyword=notification-fixture", SearchUsers},
		{"/api/user/" + strconv.Itoa(user.Id), GetUser},
	} {
		response := adminUserRequest(http.MethodGet, test.p, "", "", identity, common.RoleRootUser, gin.Params{{Key: "id", Value: strconv.Itoa(user.Id)}}, test.h)
		assert.Contains(t, response.Body.String(), user.Username)
		for _, private := range []string{"fixture-destination", "fixture-webhook-secret", "fixture-bark", "fixture-gotify"} {
			assert.False(t, strings.Contains(response.Body.String(), private))
			assert.False(t, strings.Contains(self.Body.String(), private))
		}
	}
	update := func(body string) {
		t.Helper()
		response := adminUserRequest(http.MethodPut, "/api/user/setting", body, "", service.AuthIdentity{UserID: user.Id}, common.RoleCommonUser, nil, UpdateUserSetting)
		assert.Contains(t, response.Body.String(), `"success":true`)
	}
	update(`{"notify_type":"webhook","webhook_url":"","webhook_secret":"","quota_warning_threshold":2000}`)
	stored, err := model.GetUserById(user.Id, true)
	require.NoError(t, err)
	assert.True(t, stored.GetSetting().WebhookUrl == hook && stored.GetSetting().WebhookSecret == secret)
	assert.EqualValues(t, 2000, stored.GetSetting().QuotaWarningThreshold)
	update(`{"notify_type":"webhook","clear_webhook_secret":true}`)
	stored, err = model.GetUserById(user.Id, true)
	require.NoError(t, err)
	assert.True(t, stored.GetSetting().WebhookUrl == hook)
	assert.True(t, stored.GetSetting().WebhookSecret == "")
}

func TestCoreAccountRegistrationProofRollback(t *testing.T) {
	existing, _ := setupSecurityEnrollmentTest(t)
	email, code := "registration-fixture@example.invalid", "fe33ab45"
	require.NoError(t, model.CreateRegistrationEmailCode(t.Context(), email, code, time.Minute))
	duplicate := model.User{Username: existing.Username, Password: "Fixture-password-12", Email: email}
	require.Error(t, duplicate.InsertWithRegistrationEmail(0, code))
	require.Zero(t, duplicate.Id)
	retry := model.User{Username: "registration-retry", Password: "Fixture-password-12", Email: email}
	require.NoError(t, retry.InsertWithRegistrationEmail(0, code))
	assert.True(t, model.VerifiedSupportEmail(&retry))
	other := model.User{Username: "registration-replay", Password: "Fixture-password-12", Email: email}
	assert.True(t, errors.Is(other.InsertWithRegistrationEmail(0, code), model.ErrAuthFlowConsumed))
}
