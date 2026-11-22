package service

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGoogleUnbindProofIsDistinctFromCustomProviderProof(t *testing.T) {
	google, err := BindVerificationOperation(VerificationOperation{Scope: VerificationScopeAccountUnbind, Context: json.RawMessage(`{"provider":"google"}`)})
	require.NoError(t, err)
	custom, err := BindVerificationOperation(VerificationOperation{Scope: VerificationScopeAccountUnbind, Context: json.RawMessage(`{"provider_id":3}`)})
	require.NoError(t, err)
	assert.NotEqual(t, google.ContextHash, custom.ContextHash)
	for _, context := range []string{
		`{"provider":"google","provider_id":3}`, `{"provider":"other"}`, `{"provider_id":0}`,
		`{"provider_id":null}`, `{"provider":null}`, `{"provider":"google","extra":true}`, `{"provider":" google "}`,
	} {
		_, err := BindVerificationOperation(VerificationOperation{Scope: VerificationScopeAccountUnbind, Context: json.RawMessage(context)})
		assert.ErrorIs(t, err, ErrVerificationContextInvalid, context)
	}
}

func TestGoogleUnbindInvalidatesExistingTwoFactorLoginChallenge(t *testing.T) {
	if os.Getenv("TEST_SERVICE_POSTGRES_DSN") == "" {
		t.Skip("requires isolated PostgreSQL 18.6 TEST_SERVICE_POSTGRES_DSN")
	}
	require.NoError(t, model.DB.AutoMigrate(&model.AuthFlow{}, &model.UserSession{}, &model.ExternalIdentityClaim{}, &model.TwoFA{}, &model.PasskeyCredential{}))
	user := &model.User{Username: "google-mfa-pending", AffCode: "google-mfa-pending", Password: "not-a-login-hash", Status: common.UserStatusEnabled, Role: common.RoleCommonUser, AuthVersion: 1}
	require.NoError(t, model.DB.Create(user).Error)
	t.Cleanup(func() {
		for _, record := range []any{&model.AuthFlow{}, &model.UserSession{}, &model.ExternalIdentityClaim{}, &model.TwoFA{}, &model.PasskeyCredential{}} {
			require.NoError(t, model.DB.Unscoped().Where("user_id = ?", user.Id).Delete(record).Error)
		}
		require.NoError(t, model.DB.Unscoped().Delete(user).Error)
	})
	require.NoError(t, model.DB.Transaction(func(tx *gorm.DB) error { return model.UpdateGoogleBindingWithTx(tx, user.Id, "google-mfa-subject") }))
	now := time.Now().Unix()
	session := &model.UserSession{
		SID: "google-mfa-current", UserID: user.Id, Version: 1, UserAuthVersion: 1,
		Status: model.UserSessionStatusActive, RefreshHash: "synthetic-not-a-refresh-token",
		LoginMethod: "password", CreatedAt: now, LastActiveAt: now, ExpiresAt: now + 600,
	}
	require.NoError(t, model.DB.Create(session).Error)
	require.NoError(t, model.DB.Create(&model.TwoFA{UserId: user.Id, Secret: "JBSWY3DPEHPK3PXP", IsEnabled: true}).Error)
	challenge, err := StartLoginVerification(user, "google", nil)
	require.NoError(t, err)
	require.NotNil(t, challenge)
	assert.True(t, challenge.RequireVerification)
	verification, err := RequireLoginVerification(challenge.FlowToken, VerificationMethodTwoFA)
	require.NoError(t, err)
	identity := model.AuthSessionIdentity{UserID: user.Id, SessionID: session.SID, UserAuthVersion: 1, SessionVersion: 1}
	require.NoError(t, model.UnbindGoogleForSession(identity, model.AccountLoginMethods{Password: true}))
	_, err = RequireLoginVerification(challenge.FlowToken, VerificationMethodTwoFA)
	assert.ErrorIs(t, err, model.ErrUserSessionInactive)
	_, _, err = CompleteLoginVerification(challenge.FlowToken, verification, VerificationMethodTwoFA, "127.0.0.1", "google-identity-test")
	assert.ErrorIs(t, err, model.ErrUserSessionInactive)
	var issued int64
	require.NoError(t, model.DB.Model(&model.UserSession{}).Where("user_id = ? AND sid <> ?", user.Id, session.SID).Count(&issued).Error)
	assert.Zero(t, issued)
}
