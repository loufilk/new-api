package controller

import (
	"net/http"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSupportEmailLegacyAddressRequiresProofThenSameAddressIsVerified(t *testing.T) {
	require.Equal(t, "postgres", os.Getenv("TEST_SECURITY_DIALECT"))
	user, identity := setupSecurityEnrollmentTest(t)
	mailbox := newSecurityMailbox(t)
	require.NoError(t, model.DB.Model(user).Updates(map[string]any{"email": "legacy@example.com", "email_verified_address": nil, "email_verification_version": nil, "email_verified_at": nil}).Error)
	stored, err := model.GetUserById(user.Id, false)
	require.NoError(t, err)
	assert.False(t, model.VerifiedSupportEmail(stored))
	flow := startSecurityEmailBinding(t, identity, "legacy@example.com", service.VerificationMethodPassword)
	assert.False(t, flow.OldEmailRequired, "one mailbox needs one code, without bypassing the operation proof")
	code := mailbox.code(t, flow.Email)
	_, err = service.FinishEmailBinding(identity, flow.FlowToken, "invalid", "")
	assert.ErrorIs(t, err, model.ErrEmailBindingCodeInvalid)
	stored, err = model.GetUserById(user.Id, false)
	require.NoError(t, err)
	assert.False(t, model.VerifiedSupportEmail(stored))
	_, err = service.FinishEmailBinding(identity, flow.FlowToken, code, "")
	require.NoError(t, err)
	stored, err = model.GetUserById(user.Id, false)
	require.NoError(t, err)
	assert.True(t, model.VerifiedSupportEmail(stored))
	assert.EqualValues(t, 1, stored.EmailVerificationVersion)
	assert.Equal(t, "legacy@example.com", stored.Email)
	_, err = service.FinishEmailBinding(identity, flow.FlowToken, code, "")
	assert.ErrorIs(t, err, model.ErrAuthFlowConsumed)
	encoded, err := common.Marshal(stored)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "email_verified")
	assert.NotContains(t, string(encoded), "email_verification_version")
	context, err := common.Marshal(service.AccountBindingContext{Provider: "email", Email: stored.Email})
	require.NoError(t, err)
	proof := issueSecurityEnrollmentProof(t, identity, service.VerificationOperation{Scope: service.VerificationScopeAccountBind, Context: context}, service.VerificationMethodPassword)
	response := securityEnrollmentRequest(http.MethodPost, "/api/oauth/email/bind/start", `{"email":"legacy@example.com"}`, proof, identity, EmailBindStart)
	assert.Contains(t, response.Body.String(), `"success":false`, "an already verified address must not start repeated verification mail")
}

func TestSupportEmailChangeAndUnbindInvalidateProofWithoutSnapshotResurrection(t *testing.T) {
	require.Equal(t, "postgres", os.Getenv("TEST_SECURITY_DIALECT"))
	user, identity := setupSecurityEnrollmentTest(t)
	mailbox := newSecurityMailbox(t)
	flow := startSecurityEmailBinding(t, identity, "confirmed@example.com", service.VerificationMethodPassword)
	_, err := service.FinishEmailBinding(identity, flow.FlowToken, mailbox.code(t, flow.Email), "")
	require.NoError(t, err)
	stale, err := model.GetUserById(user.Id, false)
	require.NoError(t, err)
	assert.True(t, model.VerifiedSupportEmail(stale))
	originalVersion := stale.EmailVerificationVersion
	require.NoError(t, model.BindEmailToUser(user, "replacement@example.com"))
	stored, err := model.GetUserById(user.Id, false)
	require.NoError(t, err)
	assert.False(t, model.VerifiedSupportEmail(stored))
	assert.Greater(t, stored.EmailVerificationVersion, originalVersion)
	// A stale object carrying the old proof may change a profile but cannot
	// restore proof merely by writing the previously verified address back.
	require.NoError(t, stale.Update(false))
	stored, err = model.GetUserById(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, "confirmed@example.com", stored.Email)
	assert.False(t, model.VerifiedSupportEmail(stored))
	beforeUnbind := stored.EmailVerificationVersion
	require.NoError(t, stored.ClearBinding("email"))
	assert.Empty(t, stored.Email)
	assert.Empty(t, stored.EmailVerifiedAddress)
	assert.Zero(t, stored.EmailVerifiedAt)
	assert.Greater(t, stored.EmailVerificationVersion, beforeUnbind)
}

func TestSupportEmailReverificationRejectsUnbindAndRebindDuringPendingFlow(t *testing.T) {
	require.Equal(t, "postgres", os.Getenv("TEST_SECURITY_DIALECT"))
	user, identity := setupSecurityEnrollmentTest(t)
	mailbox := newSecurityMailbox(t)
	require.NoError(t, model.DB.Model(user).Update("email", "legacy@example.com").Error)
	flow := startSecurityEmailBinding(t, identity, "legacy@example.com", service.VerificationMethodPassword)
	code := mailbox.code(t, flow.Email)
	require.NoError(t, user.ClearBinding("email"))
	require.NoError(t, model.BindEmailToUser(user, "legacy@example.com"))
	_, err := service.FinishEmailBinding(identity, flow.FlowToken, code, "")
	assert.ErrorIs(t, err, model.ErrAccountBindingChanged)
	stored, err := model.GetUserById(user.Id, false)
	require.NoError(t, err)
	assert.False(t, model.VerifiedSupportEmail(stored))
}

func TestSupportEmailRegistrationTrustRequiresConsumedVerificationCode(t *testing.T) {
	require.Equal(t, "postgres", os.Getenv("TEST_SECURITY_DIALECT"))
	_, _ = setupSecurityEnrollmentTest(t)
	previousRegister, previousPassword, previousVerify := common.RegisterEnabled, common.PasswordRegisterEnabled, common.EmailVerificationEnabled
	previousToken := constant.GenerateDefaultToken
	common.RegisterEnabled, common.PasswordRegisterEnabled, common.EmailVerificationEnabled = true, true, true
	constant.GenerateDefaultToken = false
	t.Cleanup(func() {
		common.RegisterEnabled, common.PasswordRegisterEnabled, common.EmailVerificationEnabled = previousRegister, previousPassword, previousVerify
		constant.GenerateDefaultToken = previousToken
		common.DeleteKey("register@example.com", common.EmailVerificationPurpose)
	})
	forged := `{"username":"register-user","password":"SecureRegistration123","email":"register@example.com","verification_code":"123456","email_verified_address":"register@example.com","email_verification_version":999,"email_verified_at":123}`
	rejected := securityEnrollmentRequest(http.MethodPost, "/api/user/register", forged, "", service.AuthIdentity{}, Register)
	assert.Contains(t, rejected.Body.String(), `"success":false`)
	common.RegisterVerificationCodeWithKey("register@example.com", "123456", common.EmailVerificationPurpose)
	response := securityEnrollmentRequest(http.MethodPost, "/api/user/register", forged, "", service.AuthIdentity{}, Register)
	require.Contains(t, response.Body.String(), `"success":true`)
	var stored model.User
	require.NoError(t, model.DB.Where("username = ?", "register-user").First(&stored).Error)
	assert.True(t, model.VerifiedSupportEmail(&stored))
	assert.EqualValues(t, 1, stored.EmailVerificationVersion, "client cannot set server trust metadata")
	assert.NotEqualValues(t, 123, stored.EmailVerifiedAt)
	assert.False(t, common.VerifyCodeWithKey("register@example.com", "123456", common.EmailVerificationPurpose))
}
