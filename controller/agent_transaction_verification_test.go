package controller

import (
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAgentTransactionVerificationRequiresFreshMFAAndStrictContextPostgres(t *testing.T) {
	if os.Getenv("TEST_POSTGRES_DSN") == "" {
		t.Skip("requires isolated PostgreSQL 18.6")
	}
	t.Setenv("TEST_SECURITY_DIALECT", "postgres")
	user, identity := setupSecurityEnrollmentTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.AgentProfile{}, &model.AgentPolicy{}, &model.AgentPolicyState{}))
	context, err := model.NewAgentTransactionContext("commission.withdraw.request", "controller-proof-request", user.Id, 0, 0, "USD", "20", 0, "withdrawal", "draft-1", 1, "fixed")
	require.NoError(t, err)
	encoded, err := common.Marshal(context)
	require.NoError(t, err)
	_, err = service.GetVerificationRequirements(identity, model.AgentTransactionScope)
	require.ErrorIs(t, err, model.ErrAgentTransactionMethod, "password-only account cannot authorize money")
	twoFA := model.TwoFA{UserId: user.Id, Secret: "JBSWY3DPEHPK3PXP", IsEnabled: true}
	require.NoError(t, model.DB.Create(&twoFA).Error)
	for _, method := range []string{"password", "oauth", "session"} {
		_, err := service.VerifySecurityInput(identity, service.VerificationInput{Scope: model.AgentTransactionScope, Context: encoded, Method: method, Password: "enrollment-password"})
		require.Error(t, err, method)
	}
	_, err = service.VerifySecurityInput(identity, service.VerificationInput{Scope: model.AgentTransactionScope, Context: encoded, Method: "2fa", Code: "ABCD-EFGH-IJKL"})
	require.Error(t, err, "recovery codes cannot substitute for transaction MFA")
	patIdentity := identity
	patIdentity.SessionID = model.AccessTokenSessionID(1)
	_, err = service.GetVerificationRequirements(patIdentity, model.AgentTransactionScope)
	require.ErrorIs(t, err, service.ErrVerificationForbidden)
	for _, bad := range []string{
		strings.Replace(string(encoded), `"profile_version":0`, `"unknown_version":0`, 1),
		strings.Replace(string(encoded), `"profile_version":0`, `"profile_version":null`, 1),
		strings.Replace(string(encoded), `"amount_minor":"20"`, `"amount_minor":"020"`, 1),
		strings.Replace(string(encoded), `"amount_minor":"20"`, `"amount_minor":20`, 1),
		strings.TrimSuffix(string(encoded), "}") + `,"unexpected":true}`,
	} {
		_, err = service.BindVerificationOperation(service.VerificationOperation{Scope: model.AgentTransactionScope, Context: []byte(bad)})
		require.Error(t, err, bad)
	}
	code, err := totp.GenerateCode(twoFA.Secret, time.Now())
	require.NoError(t, err)
	proof, err := service.VerifySecurityInput(identity, service.VerificationInput{Scope: model.AgentTransactionScope, Context: encoded, Method: "2fa", Code: code})
	require.NoError(t, err)
	assert.Equal(t, model.AgentTransactionScope, proof.Scope)
	assert.Len(t, proof.ProofToken, 43)
	assert.NotContains(t, proof.ProofToken, ".", "opaque proof is not a session JWT")
	_, err = service.VerifySecurityInput(identity, service.VerificationInput{Scope: model.AgentTransactionScope, Context: encoded, Method: "2fa", Code: code})
	require.Error(t, err, "same OTP cannot mint another transaction authorization")
	_, err = service.ConsumeOperationProof(proof.ProofToken, identity, service.VerificationOperation{Scope: model.AgentTransactionScope, Context: encoded})
	require.ErrorIs(t, err, service.ErrProofScope, "old early-consume path must not authorize financial actions")
	authorization, err := service.PrepareAgentTransactionAuthorization(proof.ProofToken, identity, context)
	require.NoError(t, err)
	require.NoError(t, model.DB.Transaction(func(tx *gorm.DB) error {
		return model.ConsumeAgentTransactionAuthorizationTx(tx, authorization, context)
	}))
}

func TestAgentProfileHTTPPreparationMutationAndSafeRetryPostgres(t *testing.T) {
	if os.Getenv("TEST_POSTGRES_DSN") == "" {
		t.Skip("requires isolated PostgreSQL 18.6")
	}
	t.Setenv("TEST_SECURITY_DIALECT", "postgres")
	operator, identity, target := setupAdminUserTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.AgentProfile{}, &model.AgentProfileAudit{}, &model.AgentProfileOperation{}, &model.AgentPolicy{}, &model.AgentPolicyState{}))
	require.NoError(t, model.DB.Create(&model.TwoFA{UserId: operator.Id, IsEnabled: true, Secret: "JBSWY3DPEHPK3PXP"}).Error)
	command := model.AgentProfileCommand{UserID: target.Id, AgentProfileMutation: model.AgentProfileMutation{Status: model.AgentProfileActive, Reason: "Qualify business without changing personal services"}}
	payload, err := common.Marshal(command)
	require.NoError(t, err)
	prepared, err := model.PrepareAgentProfileOperation(identity, "profile.update", "http-profile-request", payload)
	require.NoError(t, err)
	assert.Equal(t, "0", prepared.Summary.AmountMinor)
	hash, err := model.AgentTransactionContextHash(prepared.VerificationContext)
	require.NoError(t, err)
	token, _, err := model.IssueAgentTransactionProof(identity, hash, "2fa")
	require.NoError(t, err)
	body := `{"request_id":"http-profile-request","expected_version":0,"status":"active","sales_frozen":false,"settlement_frozen":false,"reason":"Qualify business without changing personal services"}`
	for _, bad := range []string{strings.TrimSuffix(body, "}") + `,"unexpected":true}`, body + ` {}`, strings.Replace(body, `"sales_frozen":false`, `"sales_frozen":null`, 1), strings.Replace(body, `"sales_frozen":false,`, "", 1)} {
		response := adminUserRequest(http.MethodPatch, "/api/agent-platform/profiles/"+prepared.VerificationContext.TargetID, bad, token, identity, common.RoleRootUser, gin.Params{{Key: "id", Value: prepared.VerificationContext.TargetID}}, UpdateAgentProfile)
		assert.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	}
	for range 2 {
		response := adminUserRequest(http.MethodPatch, "/api/agent-platform/profiles/"+prepared.VerificationContext.TargetID, body, token, identity, common.RoleRootUser, gin.Params{{Key: "id", Value: prepared.VerificationContext.TargetID}}, UpdateAgentProfile)
		assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Contains(t, response.Body.String(), `"success":true`)
	}
	profile, err := model.GetAgentProfile(target.Id)
	require.NoError(t, err)
	require.NotNil(t, profile)
	assert.EqualValues(t, 1, profile.Version)
	var audits int64
	require.NoError(t, model.DB.Model(&model.AgentProfileAudit{}).Where("user_id = ?", target.Id).Count(&audits).Error)
	assert.EqualValues(t, 1, audits)
	status, err := model.FindAgentProfileOperation(identity, "http-profile-request")
	require.NoError(t, err)
	require.NotNil(t, status)
	assert.Equal(t, "completed", status.Status)
	assert.Equal(t, "active", status.ResourceState)
	changedBody := strings.Replace(body, "Qualify business without changing personal services", "Changed reason", 1)
	response := adminUserRequest(http.MethodPatch, "/api/agent-platform/profiles/"+prepared.VerificationContext.TargetID, changedBody, token, identity, common.RoleRootUser, gin.Params{{Key: "id", Value: prepared.VerificationContext.TargetID}}, UpdateAgentProfile)
	assert.Equal(t, http.StatusConflict, response.Code, response.Body.String())
}

func TestAgentTransactionTOTPReplayAcrossSessionsPostgres(t *testing.T) {
	if os.Getenv("TEST_POSTGRES_DSN") == "" {
		t.Skip("requires isolated PostgreSQL 18.6")
	}
	t.Setenv("TEST_SECURITY_DIALECT", "postgres")
	user, first := setupSecurityEnrollmentTest(t)
	factor := model.TwoFA{UserId: user.Id, Secret: "JBSWY3DPEHPK3PXP", IsEnabled: true}
	require.NoError(t, model.DB.Create(&factor).Error)
	second := first
	second.SessionID = "transaction-second-session"
	require.NoError(t, model.DB.Create(&model.UserSession{SID: second.SessionID, UserID: user.Id, Version: second.SessionVersion, UserAuthVersion: second.UserAuthVersion, Status: model.UserSessionStatusActive, ExpiresAt: time.Now().Add(time.Hour).Unix(), RefreshHash: "synthetic-second-session", LoginMethod: "password"}).Error)
	code, err := totp.GenerateCode(factor.Secret, time.Now())
	require.NoError(t, err)
	inputs := make([]service.VerificationInput, 2)
	for i, requestID := range []string{"otp-replay-session-one", "otp-replay-session-two"} {
		context, err := model.NewAgentTransactionContext("commission.withdraw.request", requestID, user.Id, 0, 0, "USD", "20", 0, "withdrawal", requestID, 1, "fixed")
		require.NoError(t, err)
		encoded, err := common.Marshal(context)
		require.NoError(t, err)
		inputs[i] = service.VerificationInput{Scope: model.AgentTransactionScope, Context: encoded, Method: "2fa", Code: code}
	}
	var workers sync.WaitGroup
	start := make(chan struct{})
	outcomes := make(chan error, 2)
	for i, identity := range []model.AuthSessionIdentity{first, second} {
		workers.Go(func() { <-start; _, err := service.VerifySecurityInput(identity, inputs[i]); outcomes <- err })
	}
	close(start)
	workers.Wait()
	close(outcomes)
	successes := 0
	for err := range outcomes {
		if err == nil {
			successes++
		} else {
			assert.ErrorIs(t, err, service.ErrVerificationFailed)
		}
	}
	require.Equal(t, 1, successes, "one TOTP may authorize only one transaction across browser sessions and database connections")
	var flows int64
	require.NoError(t, model.DB.Model(&model.AuthFlow{}).Where("purpose = ?", "agent_transaction").Count(&flows).Error)
	assert.EqualValues(t, 1, flows)
}
