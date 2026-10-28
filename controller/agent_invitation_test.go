package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupAgentInvitationControllerTest(t *testing.T) []model.User {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires disposable loopback PostgreSQL 18.6 TEST_POSTGRES_DSN")
	}
	db, _ := newAuditTestDatabase(t, "postgres", dsn)
	var version string
	require.NoError(t, db.Raw("SHOW server_version_num").Scan(&version).Error)
	require.Equal(t, "180006", version)
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	previousRedis := common.RedisEnabled
	previousPayment := *operation_setting.GetPaymentSetting()
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	common.RedisEnabled = false
	operation_setting.GetPaymentSetting().ComplianceConfirmed = true
	operation_setting.GetPaymentSetting().ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMain, previousLog)
		common.RedisEnabled = previousRedis
		*operation_setting.GetPaymentSetting() = previousPayment
	})
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.AgentInvitation{}, &model.AgentInvitationPolicy{}, &model.AgentInvitationPolicyAudit{}, &model.BalanceLedgerOperation{}, &model.BalanceLedger{}, &model.Log{}))
	users := []model.User{
		{Username: "invite-common", Role: common.RoleCommonUser},
		{Username: "invite-agent-a", Role: common.RoleAgentUser},
		{Username: "invite-agent-b", Role: common.RoleAgentUser},
		{Username: "invite-admin", Role: common.RoleAdminUser},
		{Username: "invite-root", Role: common.RoleRootUser},
	}
	for i := range users {
		users[i].Status = common.UserStatusEnabled
		users[i].AffCode = fmt.Sprintf("invitation-api-%d", i)
		require.NoError(t, db.Create(&users[i]).Error)
	}
	return users
}

func performAgentInvitationRequest(t *testing.T, user model.User, method, target, body string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", user.Id)
	// Deliberately stale/forged context role: model authorization must reread DB.
	c.Set("role", common.RoleRootUser)
	handler(c)
	return recorder
}

func TestAgentInvitationAPIEnforcesActorAndPrivateResponse(t *testing.T) {
	users := setupAgentInvitationControllerTest(t)
	_, err := model.UpdateAgentInvitationPolicy(users[4].Id, true, 300)
	require.NoError(t, err)
	for i, inviter := range users[1:3] {
		user := &model.User{Username: fmt.Sprintf("invitee-%d", i), Email: fmt.Sprintf("sensitive-%d@example.test", i), Password: "Test-only-password-928!"}
		require.NoError(t, user.Insert(inviter.Id))
	}
	for i, user := range users {
		response := performAgentInvitationRequest(t, user, "GET", fmt.Sprintf("/api/agent-redemption/invitations?inviter_id=%d&user_id=%d&p=-3&page_size=-20", users[2].Id, users[2].Id), "", GetAgentInvitations)
		if i == 0 {
			assert.Equal(t, http.StatusForbidden, response.Code)
			continue
		}
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var payload struct {
			Success bool `json:"success"`
			Data    struct {
				Page, PageSize, Total int
				Items                 []model.AgentInvitation `json:"items"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
		require.True(t, payload.Success)
		require.Len(t, payload.Data.Items, 1)
		assert.Equal(t, 1, payload.Data.Page)
		if user.Role == common.RoleAgentUser {
			assert.Equal(t, 1, payload.Data.Total)
			assert.Equal(t, user.Id, payload.Data.Items[0].InviterId)
		} else {
			assert.Equal(t, 2, payload.Data.Total)
		}
		for _, secret := range []string{"password", "email", "access_token", "aff_code", "quota_paid", "quota_reward"} {
			assert.NotContains(t, response.Body.String(), `"`+secret+`":`)
		}
		assert.NotContains(t, response.Body.String(), "sensitive-")
		assert.NotContains(t, response.Body.String(), "Test-only-password")
	}
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", users[1].Id).Update("status", common.UserStatusDisabled).Error)
	response := performAgentInvitationRequest(t, users[1], "GET", "/api/agent-redemption/invitations", "", GetAgentInvitations)
	assert.Equal(t, http.StatusForbidden, response.Code)
}

func TestAgentInvitationPolicyAPIRootOnlyAndBounds(t *testing.T) {
	users := setupAgentInvitationControllerTest(t)
	for _, user := range users[:4] {
		for _, call := range []struct {
			method  string
			handler gin.HandlerFunc
		}{{"GET", GetAgentInvitationPolicy}, {"PUT", UpdateAgentInvitationPolicy}} {
			response := performAgentInvitationRequest(t, user, call.method, "/api/agent-redemption/invitation-policy", fmt.Sprintf(`{"enabled":true,"quota":400,"actor_id":%d,"role":100}`, users[4].Id), call.handler)
			assert.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
		}
	}
	for _, body := range []string{`{}`, `{"enabled":true}`, `{"quota":0}`, `{"enabled":true,"quota":-1}`, `{"enabled":true,"quota":9007199254740992}`, `{"enabled":true,"quota":0.5}`, `{"enabled":true,"quota":"400"}`} {
		response := performAgentInvitationRequest(t, users[4], "PUT", "/api/agent-redemption/invitation-policy", body, UpdateAgentInvitationPolicy)
		assert.Equal(t, http.StatusBadRequest, response.Code, body)
	}
	response := performAgentInvitationRequest(t, users[4], "PUT", "/api/agent-redemption/invitation-policy", `{"enabled":true,"quota":400,"updated_by":1}`, UpdateAgentInvitationPolicy)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	policy, err := model.GetAgentInvitationPolicy(users[4].Id)
	require.NoError(t, err)
	assert.Equal(t, users[4].Id, policy.UpdatedBy)
	assert.Equal(t, 400, policy.Quota)
}

func TestAgentInvitationPasswordRegistrationIgnoresForgedFields(t *testing.T) {
	users := setupAgentInvitationControllerTest(t)
	previousRegister, previousPassword, previousVerification := common.RegisterEnabled, common.PasswordRegisterEnabled, common.EmailVerificationEnabled
	previousDefaultToken := constant.GenerateDefaultToken
	common.RegisterEnabled, common.PasswordRegisterEnabled, common.EmailVerificationEnabled = true, true, false
	constant.GenerateDefaultToken = false
	t.Cleanup(func() {
		common.RegisterEnabled, common.PasswordRegisterEnabled, common.EmailVerificationEnabled = previousRegister, previousPassword, previousVerification
		constant.GenerateDefaultToken = previousDefaultToken
	})
	_, err := model.UpdateAgentInvitationPolicy(users[4].Id, true, 900)
	require.NoError(t, err)
	body := fmt.Sprintf(`{"username":"public-invitee","password":"Test-only-password-984!","aff_code":%q,"inviter_id":%d,"role":100,"quota":99999,"quota_bonus":99999,"invitation":{"inviter_role":100,"bonus_quota":99999}}`, users[1].AffCode, users[2].Id)
	response := performAgentInvitationRequest(t, model.User{}, "POST", "/api/user/register", body, Register)
	require.Equal(t, http.StatusOK, response.Code)
	var payload struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
	require.True(t, payload.Success, response.Body.String())
	var user model.User
	require.NoError(t, model.DB.Where("username = ?", "public-invitee").First(&user).Error)
	assert.Equal(t, common.RoleCommonUser, user.Role)
	assert.Equal(t, users[1].Id, user.InviterId)
	assert.Equal(t, 900, user.QuotaBonus)
	assert.Zero(t, user.QuotaPaid)
	var invitation model.AgentInvitation
	require.NoError(t, model.DB.Where("user_id = ?", user.Id).First(&invitation).Error)
	assert.Equal(t, common.RoleAgentUser, invitation.InviterRole)
	assert.Equal(t, model.InvitationSourcePassword, invitation.Source)
	response = performAgentInvitationRequest(t, model.User{}, "POST", "/api/user/register", body, Register)
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
	assert.False(t, payload.Success)
	var count int64
	require.NoError(t, model.DB.Model(&model.AgentInvitation{}).Where("user_id = ?", user.Id).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}
