package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func withdrawalControllerFixture(t *testing.T) (service.AuthIdentity, service.AuthIdentity, service.AuthIdentity, model.ReferralWithdrawalConfig) {
	t.Helper()
	require.Equal(t, "postgres", os.Getenv("TEST_SECURITY_DIALECT"), "requires isolated PostgreSQL 18.6")
	user, identity := setupSecurityEnrollmentTest(t)
	var version string
	require.NoError(t, model.DB.Raw("SHOW server_version_num").Scan(&version).Error)
	require.Equal(t, "180006", version)
	require.NoError(t, model.DB.AutoMigrate(append(model.ReferralWithdrawalModels(), &model.ReferralCommission{}, &model.CasbinRule{}, &model.AuthzRole{})...))
	require.NoError(t, authz.Init(model.DB))
	require.NoError(t, model.DB.Model(user).Updates(map[string]any{"aff_quota": 5_000_000, "aff_history": 5_000_000, "used_quota": 10_000_000}).Error)
	identities := []service.AuthIdentity{identity}
	for _, role := range []int{common.RoleAdminUser, common.RoleRootUser} {
		actor := model.User{Username: fmt.Sprintf("withdrawal-controller-%d", role), AffCode: fmt.Sprintf("withdraw-controller-%d", role), Role: role, Status: common.UserStatusEnabled, AuthVersion: 1, Password: user.Password}
		require.NoError(t, model.DB.Create(&actor).Error)
		actorIdentity := service.AuthIdentity{UserID: actor.Id, SessionID: fmt.Sprintf("withdrawal-controller-session-%d", actor.Id), UserAuthVersion: 1, SessionVersion: 1}
		require.NoError(t, model.DB.Create(&model.UserSession{SID: actorIdentity.SessionID, UserID: actor.Id, UserAuthVersion: 1, Version: 1, Status: model.UserSessionStatusActive, ExpiresAt: time.Now().Add(time.Hour).Unix(), RefreshHash: "controller-test-only", LoginMethod: "password"}).Error)
		identities = append(identities, actorIdentity)
	}
	config := model.DefaultReferralWithdrawalConfig()
	config.Enabled, config.MinWithdrawalQuota = true, 500_000
	saved, err := model.SaveReferralWithdrawalConfig(context.Background(), identities[2], config)
	require.NoError(t, err)
	return identities[0], identities[1], identities[2], *saved
}

func withdrawalControllerRequest(identity service.AuthIdentity, id int64, body, key, proof string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/referral-withdrawals", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Idempotency-Key", key)
	c.Request.Header.Set("X-Security-Proof", proof)
	c.Set("id", identity.UserID)
	c.Set("role", common.RoleRootUser)
	c.Set("session_id", identity.SessionID)
	c.Set("auth_version", identity.UserAuthVersion)
	c.Set("session_version", identity.SessionVersion)
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(id, 10)}}
	handler(c)
	return response
}

func withdrawalProof(t *testing.T, identity service.AuthIdentity, scope string, input any) string {
	t.Helper()
	encoded, err := common.Marshal(input)
	require.NoError(t, err)
	return issueSecurityEnrollmentProof(t, identity, service.VerificationOperation{Scope: scope, Context: encoded}, service.VerificationMethodPassword)
}

func TestReferralWithdrawalAPIProofReplayOwnershipAndPermissions(t *testing.T) {
	user, admin, root, config := withdrawalControllerFixture(t)
	input := model.ReferralWithdrawalInput{AmountQuota: 1_000_000, Network: "BSC", Address: "0x1111111111111111111111111111111111111111", PolicyRevision: config.Revision}
	body, err := common.Marshal(input)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, withdrawalControllerRequest(user, 0, string(body), "http-create", "", CreateReferralWithdrawal).Code)
	contextInput := service.ReferralWithdrawalCreateContext{RequestKey: "http-create", Input: input}
	proof := withdrawalProof(t, user, service.VerificationScopeReferralWithdrawalCreate, contextInput)
	tampered := input
	tampered.AmountQuota++
	tamperedBody, err := common.Marshal(tampered)
	require.NoError(t, err)
	response := withdrawalControllerRequest(user, 0, string(tamperedBody), "http-create", proof, CreateReferralWithdrawal)
	assert.Equal(t, http.StatusForbidden, response.Code)
	assert.Contains(t, response.Body.String(), "SECURITY_PROOF_CONTEXT_MISMATCH")
	response = withdrawalControllerRequest(user, 0, string(body), "http-create", proof, CreateReferralWithdrawal)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	assert.NotContains(t, response.Body.String(), "request_hash")
	var created struct {
		Data model.ReferralWithdrawal `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &created))
	response = withdrawalControllerRequest(user, 0, string(body), "http-create", proof, CreateReferralWithdrawal)
	assert.Equal(t, http.StatusForbidden, response.Code)
	assert.Contains(t, response.Body.String(), "SECURITY_PROOF_CONSUMED")
	proof = withdrawalProof(t, user, service.VerificationScopeReferralWithdrawalCreate, contextInput)
	response = withdrawalControllerRequest(user, 0, string(body), "http-create", proof, CreateReferralWithdrawal)
	require.Equal(t, http.StatusOK, response.Code)
	var count int64
	require.NoError(t, model.DB.Model(&model.ReferralWithdrawal{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	assert.Equal(t, http.StatusNotFound, withdrawalControllerRequest(root, created.Data.ID, "", "", "", GetReferralWithdrawal).Code)
	assert.Equal(t, http.StatusForbidden, withdrawalControllerRequest(user, 0, "", "", "", ListAdminReferralWithdrawals).Code)
	require.NoError(t, authz.SetUserPermissions(admin.UserID, authz.PermissionsMap{"referral_withdrawals": {"read": true, "review": false, "pay": false}, "referral_withdrawal_settings": {"read": false, "update": false}}))
	assert.Equal(t, http.StatusOK, withdrawalControllerRequest(admin, created.Data.ID, "", "", "", GetAdminReferralWithdrawal).Code)
	assert.Equal(t, http.StatusForbidden, withdrawalControllerRequest(admin, created.Data.ID, `{"expected_revision":1,"action":"approve"}`, "admin-no-grant", "", ReviewReferralWithdrawal).Code)
	assert.Equal(t, http.StatusForbidden, withdrawalControllerRequest(admin, 0, "", "", "", GetReferralWithdrawalConfig).Code)
	action := model.ReferralWithdrawalAction{ExpectedRevision: 1, Action: "approve"}
	actionBody, err := common.Marshal(action)
	require.NoError(t, err)
	proof = withdrawalProof(t, root, service.VerificationScopeReferralWithdrawalReview, service.ReferralWithdrawalReviewContext{WithdrawalID: created.Data.ID, RequestKey: "http-approve", Input: action})
	response = withdrawalControllerRequest(root, created.Data.ID, string(actionBody), "http-approve", proof, ReviewReferralWithdrawal)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, http.StatusForbidden, withdrawalControllerRequest(root, created.Data.ID, `{"expected_revision":2,"action":"paid"}`, "wrong-route", "", ReviewReferralWithdrawal).Code)
	assert.Equal(t, http.StatusForbidden, withdrawalControllerRequest(root, created.Data.ID, `{"expected_revision":2,"action":"reject","note":"Reason"}`, "wrong-pay-route", "", PayReferralWithdrawal).Code)
}

func TestReferralWithdrawalAPIRejectsRevokedExpiredAndTokenSessions(t *testing.T) {
	user, _, root, config := withdrawalControllerFixture(t)
	assert.Equal(t, http.StatusOK, withdrawalControllerRequest(user, 0, "", "", "", GetReferralWithdrawalSummary).Code)
	token := user
	token.SessionID = "pat:7"
	assert.Equal(t, http.StatusUnauthorized, withdrawalControllerRequest(token, 0, "", "", "", GetReferralWithdrawalSummary).Code)
	require.NoError(t, model.DB.Model(&model.UserSession{}).Where("sid = ?", user.SessionID).Update("expires_at", time.Now().Add(-time.Minute).Unix()).Error)
	assert.Equal(t, http.StatusUnauthorized, withdrawalControllerRequest(user, 0, "", "", "", GetReferralWithdrawalSummary).Code)
	require.NoError(t, model.DB.Model(&model.UserSession{}).Where("sid = ?", root.SessionID).Update("status", model.UserSessionStatusRevoked).Error)
	assert.Equal(t, http.StatusUnauthorized, withdrawalControllerRequest(root, 0, "", "", "", GetReferralWithdrawalConfig).Code)
	body, err := common.Marshal(config)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, withdrawalControllerRequest(root, 0, string(body), "", "", UpdateReferralWithdrawalConfig).Code)
}

func TestReferralWithdrawalAPIRechecksRevokedGrantBeforeFinancialTransaction(t *testing.T) {
	user, admin, root, config := withdrawalControllerFixture(t)
	ctx := context.Background()
	row, err := model.CreateReferralWithdrawal(ctx, user, "review-create", model.ReferralWithdrawalInput{AmountQuota: 1000000, Network: "BSC", Address: "0x1111111111111111111111111111111111111111", PolicyRevision: config.Revision})
	require.NoError(t, err)
	row, err = model.ReviewReferralWithdrawal(ctx, root, row.ID, "review-approve", model.ReferralWithdrawalAction{ExpectedRevision: row.Revision, Action: "approve"}, true)
	require.NoError(t, err)
	row, err = model.ReviewReferralWithdrawal(ctx, root, row.ID, "review-begin", model.ReferralWithdrawalAction{ExpectedRevision: row.Revision, Action: "begin_payment"}, true)
	require.NoError(t, err)
	require.NoError(t, authz.SetUserPermissions(admin.UserID, authz.PermissionsMap{"referral_withdrawals": {"pay": true}}))
	require.True(t, authz.Can(admin.UserID, common.RoleAdminUser, authz.ReferralWithdrawalPay))
	action := model.ReferralWithdrawalAction{ExpectedRevision: row.Revision, Action: "paid", TransactionHash: "0x" + strings.Repeat("d", 64)}
	payload, err := common.Marshal(action)
	require.NoError(t, err)
	proof := withdrawalProof(t, admin, service.VerificationScopeReferralWithdrawalReview, service.ReferralWithdrawalReviewContext{WithdrawalID: row.ID, RequestKey: "review-paid", Input: action})
	revoked := false
	const hook = "review:withdrawal-rbac-revocation"
	require.NoError(t, model.DB.Callback().Query().After("gorm:query").Register(hook, func(tx *gorm.DB) {
		if revoked || tx.Statement.Table != "referral_withdrawals" {
			return
		}
		// This is ReviewReferralWithdrawal's first ownership lookup. HTTP
		// permission validation and one-time proof consumption already ran.
		revoked = true
		require.NoError(t, authz.SetUserPermissions(admin.UserID, authz.PermissionsMap{"referral_withdrawals": {"pay": false}}))
		require.False(t, authz.Can(admin.UserID, common.RoleAdminUser, authz.ReferralWithdrawalPay))
	}))
	defer model.DB.Callback().Query().Remove(hook)
	response := withdrawalControllerRequest(admin, row.ID, string(payload), "review-paid", proof, PayReferralWithdrawal)
	require.True(t, revoked)
	require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	stored, err := model.GetReferralWithdrawal(ctx, root, row.ID, true)
	require.NoError(t, err)
	assert.Equal(t, model.ReferralWithdrawalPaying, stored.Status)
	assert.Empty(t, stored.TransactionHash)
	var owner model.User
	require.NoError(t, model.DB.First(&owner, user.UserID).Error)
	assert.Equal(t, 5_000_000, owner.AffQuota)
}
