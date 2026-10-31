package controller

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/oauth"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
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
	require.NoError(t, db.AutoMigrate(append([]any{&model.User{}, &model.UserSession{}, &model.AgentInvitation{}, &model.AgentInvitationPolicy{}, &model.AgentInvitationPolicyAudit{}, &model.AgentProfile{}, &model.AgentProfileOperation{}, &model.AgentPolicy{}, &model.AgentPolicyState{}, &model.AgentInvitationPolicyOperation{}, &model.TwoFA{}, &model.PasskeyCredential{}, &model.AuthFlow{}, &model.BalanceLedgerOperation{}, &model.BalanceLedger{}, &model.Log{}}, model.BonusSourceModels()...)...))
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
		if users[i].Role == common.RoleAgentUser {
			require.NoError(t, db.Create(&model.AgentProfile{UserID: users[i].Id, Status: model.AgentProfileActive, Version: 1}).Error)
		}
		require.NoError(t, model.CreateUserSession(&model.UserSession{
			SID: fmt.Sprintf("invitation-api-session-%d", users[i].Id), UserID: users[i].Id,
			UserAuthVersion: users[i].AuthVersion, Version: 1,
			RefreshHash: fmt.Sprintf("synthetic-invitation-session-hash-%d", i),
			LoginMethod: "password", ExpiresAt: time.Now().Add(time.Hour).Unix(),
		}))
	}
	return users
}

func performAgentInvitationRequest(t *testing.T, user model.User, method, target, body string, handler gin.HandlerFunc, proof ...string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	if len(proof) > 0 {
		c.Request.Header.Set("X-Security-Proof", proof[0])
	}
	c.Set("id", user.Id)
	// Deliberately stale/forged context role: model authorization must reread DB.
	c.Set("role", common.RoleRootUser)
	if user.Id > 0 {
		c.Set("session_id", fmt.Sprintf("invitation-api-session-%d", user.Id))
		c.Set("auth_version", user.AuthVersion)
		c.Set("session_version", int64(1))
	}
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
	root := users[4]
	require.NoError(t, model.DB.Create(&model.TwoFA{UserId: root.Id, Secret: "synthetic-test-factor", IsEnabled: true}).Error)
	identity := model.AuthSessionIdentity{UserID: root.Id, SessionID: fmt.Sprintf("invitation-api-session-%d", root.Id), UserAuthVersion: root.AuthVersion, SessionVersion: 1}
	prepared, err := model.PrepareAgentProfileOperation(identity, "invitation.policy.update", "invitation-policy-update", []byte(`{"expected_revision":0,"enabled":true,"quota":400}`))
	require.NoError(t, err)
	hash, err := model.AgentTransactionContextHash(prepared.VerificationContext)
	require.NoError(t, err)
	proof, _, err := model.IssueAgentTransactionProof(identity, hash, "2fa")
	require.NoError(t, err)
	body := `{"request_id":"invitation-policy-update","expected_revision":0,"enabled":true,"quota":400}`
	response := performAgentInvitationRequest(t, root, "PUT", "/api/agent-redemption/invitation-policy", body, UpdateAgentInvitationPolicy)
	assert.Equal(t, http.StatusForbidden, response.Code, "MFA required even for root browser session")
	for range 2 {
		response = performAgentInvitationRequest(t, root, "PUT", "/api/agent-redemption/invitation-policy", body, UpdateAgentInvitationPolicy, proof)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	}
	response = performAgentInvitationRequest(t, root, "PUT", "/api/agent-redemption/invitation-policy", strings.Replace(body, `"quota":400`, `"quota":401`, 1), UpdateAgentInvitationPolicy, proof)
	assert.Equal(t, http.StatusConflict, response.Code, "same request cannot authorize changed gift policy")

	policy, err := model.GetAgentInvitationPolicy(users[4].Id)
	require.NoError(t, err)
	assert.Equal(t, users[4].Id, policy.UpdatedBy)
	assert.Equal(t, 400, policy.Quota)
}

func TestAgentInvitationPolicyAPIRequiresLiveBrowserIdentity(t *testing.T) {
	users := setupAgentInvitationControllerTest(t)
	root := users[4]
	for _, sessionID := range []string{"", "pat:99"} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest("PUT", "/api/agent-redemption/invitation-policy", strings.NewReader(`{"enabled":true,"quota":100}`))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set("id", root.Id)
		c.Set("role", root.Role)
		c.Set("session_id", sessionID)
		c.Set("auth_version", root.AuthVersion)
		c.Set("session_version", int64(1))
		UpdateAgentInvitationPolicy(c)
		assert.Equal(t, http.StatusForbidden, recorder.Code)
	}
	revoked, err := model.RevokeUserSession(root.Id, fmt.Sprintf("invitation-api-session-%d", root.Id), "test_logout")
	require.NoError(t, err)
	require.True(t, revoked)
	response := performAgentInvitationRequest(t, root, "PUT", "/api/agent-redemption/invitation-policy", `{"enabled":true,"quota":100}`, UpdateAgentInvitationPolicy)
	assert.Equal(t, http.StatusForbidden, response.Code)
	policy, err := model.GetAgentInvitationPolicy(root.Id)
	require.NoError(t, err)
	assert.False(t, policy.Enabled)
	assert.Zero(t, policy.Quota)
	assert.Zero(t, policy.Revision)
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

func TestAgentInvitationOAuthFlowGrantsOnlyOnVerifiedNewAccount(t *testing.T) {
	if os.Getenv("TEST_POSTGRES_DSN") == "" {
		t.Skip("requires disposable loopback PostgreSQL 18.6 TEST_POSTGRES_DSN")
	}
	t.Setenv("TEST_SECURITY_DIALECT", "postgres")
	root, _ := setupLinuxDOSignupTest(t)
	var version string
	require.NoError(t, model.DB.Raw("SHOW server_version_num").Scan(&version).Error)
	require.Equal(t, "180006", version)
	require.NoError(t, model.DB.AutoMigrate(&model.AgentInvitation{}, &model.AgentInvitationPolicy{}, &model.AgentInvitationPolicyAudit{}, &model.AgentProfile{}, &model.AgentPolicy{}, &model.AgentPolicyState{}))
	require.NoError(t, model.DB.Model(root).Update("role", common.RoleRootUser).Error)
	agent := model.User{Username: "oauth-agent", AffCode: "oauth-agent-aff", Role: common.RoleAgentUser, Status: common.UserStatusEnabled}
	require.NoError(t, model.DB.Create(&agent).Error)
	require.NoError(t, model.DB.Create(&model.AgentProfile{UserID: agent.Id, Status: model.AgentProfileActive, Version: 1}).Error)
	previousPayment := *operation_setting.GetPaymentSetting()
	operation_setting.GetPaymentSetting().ComplianceConfirmed = true
	operation_setting.GetPaymentSetting().ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
	t.Cleanup(func() { *operation_setting.GetPaymentSetting() = previousPayment })
	_, err := model.UpdateAgentInvitationPolicy(root.Id, true, 123)
	require.NoError(t, err)
	response := securityEnrollmentRequest("POST", "/api/oauth/state", fmt.Sprintf(`{"provider":"linuxdo","intent":"login","aff":%q}`, agent.AffCode), "", service.AuthIdentity{}, GenerateOAuthCode)
	var stateBody struct {
		Success bool `json:"success"`
		Data    struct {
			FlowToken string `json:"flow_token"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &stateBody))
	require.True(t, stateBody.Success, response.Body.String())
	state := stateBody.Data.FlowToken
	require.NotEmpty(t, state)
	response = linuxDOSignupCallback(state, service.AuthIdentity{})
	var body struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
	require.True(t, body.Success, response.Body.String())
	var user model.User
	require.NoError(t, model.DB.Where("linux_do_id = ?", "77101").First(&user).Error)
	assert.Equal(t, agent.Id, user.InviterId)
	assert.Equal(t, 1500123, user.QuotaBonus)
	assert.Equal(t, http.StatusForbidden, linuxDOSignupCallback(state, service.AuthIdentity{}).Code)
	// A fresh, valid login flow carrying a different invitation cannot rewrite
	// an existing account or earn another test grant.
	response = securityEnrollmentRequest("POST", "/api/oauth/state", fmt.Sprintf(`{"provider":"linuxdo","intent":"login","aff":%q}`, root.AffCode), "", service.AuthIdentity{}, GenerateOAuthCode)
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &stateBody))
	require.True(t, stateBody.Success)
	response = linuxDOSignupCallback(stateBody.Data.FlowToken, service.AuthIdentity{})
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
	require.True(t, body.Success, response.Body.String())
	require.NoError(t, model.DB.First(&user, user.Id).Error)
	assert.Equal(t, agent.Id, user.InviterId)
	assert.Equal(t, 1500123, user.QuotaBonus)
	var invitations []model.AgentInvitation
	require.NoError(t, model.DB.Find(&invitations).Error)
	require.Len(t, invitations, 1)
	assert.Equal(t, model.InvitationSourceLinuxDO, invitations[0].Source)
	assert.Equal(t, 123, invitations[0].BonusQuota)
	var count int64
	require.NoError(t, model.DB.Model(&model.BalanceLedger{}).Where("reason = ?", model.LedgerReasonAgentInvitationBonus).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestAgentInvitationLookupFailureCannotCreateUnattributedUser(t *testing.T) {
	for _, source := range []string{"password", "email", "oauth"} {
		t.Run(source, func(t *testing.T) {
			users := setupAgentInvitationControllerTest(t)
			require.NoError(t, model.DB.AutoMigrate(&model.ExternalIdentityClaim{}))
			previousRegister, previousPassword, previousVerification := common.RegisterEnabled, common.PasswordRegisterEnabled, common.EmailVerificationEnabled
			previousDefaultToken := constant.GenerateDefaultToken
			common.RegisterEnabled, common.PasswordRegisterEnabled, common.EmailVerificationEnabled = true, true, source == "email"
			constant.GenerateDefaultToken = false
			t.Cleanup(func() {
				common.RegisterEnabled, common.PasswordRegisterEnabled, common.EmailVerificationEnabled = previousRegister, previousPassword, previousVerification
				constant.GenerateDefaultToken = previousDefaultToken
			})
			_, err := model.UpdateAgentInvitationPolicy(users[4].Id, true, 222)
			require.NoError(t, err)
			failure := errors.New("synthetic invitation lookup outage")
			callback := "agent-invitation-aff-lookup-failure"
			require.NoError(t, model.DB.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table == "users" && strings.Contains(fmt.Sprint(tx.Statement.Clauses["WHERE"].Expression), "aff_code =") {
					tx.AddError(failure)
				}
			}))
			t.Cleanup(func() { _ = model.DB.Callback().Query().Remove(callback) })
			username := "lookup-" + source
			body := fmt.Sprintf(`{"username":%q,"password":"Test-only-password-782!","aff_code":%q}`, username, users[1].AffCode)
			if source == "email" {
				email, code := "invitation-lookup@example.test", "synthetic-email-code"
				common.RegisterVerificationCodeWithKey(email, code, common.EmailVerificationPurpose)
				t.Cleanup(func() { common.DeleteKey(email, common.EmailVerificationPurpose) })
				body = fmt.Sprintf(`{"username":%q,"password":"Test-only-password-782!","aff_code":%q,"email":%q,"verification_code":%q}`, username, users[1].AffCode, email, code)
			}
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Request = httptest.NewRequest("GET", "/api/oauth/linuxdo", nil)
			providerUser := &oauth.OAuthUser{ProviderUserID: "992001", Username: username}
			if source != "oauth" {
				response := performAgentInvitationRequest(t, model.User{}, "POST", "/api/user/register", body, Register)
				var payload struct {
					Success bool `json:"success"`
				}
				require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
				assert.False(t, payload.Success, response.Body.String())
			} else {
				user, _, err := findOrCreateOAuthUser(context, &oauth.LinuxDOProvider{}, providerUser, nil, users[1].AffCode)
				require.ErrorIs(t, err, failure)
				assert.Nil(t, user)
			}
			var count int64
			require.NoError(t, model.DB.Model(&model.User{}).Where("username = ?", username).Count(&count).Error)
			assert.Zero(t, count)
			for _, entity := range []any{&model.AgentInvitation{}, &model.BalanceLedger{}} {
				require.NoError(t, model.DB.Model(entity).Count(&count).Error)
				assert.Zero(t, count)
			}
			require.NoError(t, model.DB.Callback().Query().Remove(callback))
			if source != "oauth" {
				response := performAgentInvitationRequest(t, model.User{}, "POST", "/api/user/register", body, Register)
				var payload struct {
					Success bool `json:"success"`
				}
				require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
				require.True(t, payload.Success, response.Body.String())
			} else {
				_, _, err := findOrCreateOAuthUser(context, &oauth.LinuxDOProvider{}, providerUser, nil, users[1].AffCode)
				require.NoError(t, err)
			}
			var invitation model.AgentInvitation
			require.NoError(t, model.DB.First(&invitation).Error)
			assert.Equal(t, users[1].Id, invitation.InviterId)
			assert.Equal(t, 222, invitation.BonusQuota)
		})
	}
}
