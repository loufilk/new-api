package controller

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/oauth"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupLinuxDOSignupTest(t *testing.T) (*model.User, service.AuthIdentity) {
	t.Helper()
	user, identity := setupSecurityEnrollmentTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.BalanceLedgerOperation{}, &model.BalanceLedger{}))
	require.NoError(t, model.LOG_DB.AutoMigrate(&model.Log{}))
	previousEnabled, previousRegister := common.LinuxDOOAuthEnabled, common.RegisterEnabled
	previousTrust, previousUnit := common.LinuxDOMinimumTrustLevel, common.QuotaPerUnit
	previousBonus, previousQuota := common.SignupBonusEnabled, common.QuotaForSignupBonus
	common.LinuxDOOAuthEnabled, common.RegisterEnabled = true, true
	common.LinuxDOMinimumTrustLevel, common.QuotaPerUnit = 1, 500000
	// Old global options must not determine either eligibility or amount.
	common.SignupBonusEnabled, common.QuotaForSignupBonus = true, 999
	t.Cleanup(func() {
		common.LinuxDOOAuthEnabled, common.RegisterEnabled = previousEnabled, previousRegister
		common.LinuxDOMinimumTrustLevel, common.QuotaPerUnit = previousTrust, previousUnit
		common.SignupBonusEnabled, common.QuotaForSignupBonus = previousBonus, previousQuota
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/token" {
			_, _ = w.Write([]byte(`{"access_token":"synthetic-linuxdo-token"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":77101,"username":"linuxdo-new-user","name":"LinuxDO User","trust_level":2,"active":true}`))
	}))
	t.Cleanup(server.Close)
	t.Setenv("LINUX_DO_TOKEN_ENDPOINT", server.URL+"/token")
	t.Setenv("LINUX_DO_USER_ENDPOINT", server.URL+"/user")
	return user, identity
}

func linuxDOSignupState(t *testing.T, intent, proof string, identity service.AuthIdentity) string {
	t.Helper()
	response := securityEnrollmentRequest("POST", "/api/oauth/state", fmt.Sprintf(`{"provider":"linuxdo","intent":%q}`, intent), proof, identity, GenerateOAuthCode)
	var body struct {
		Success bool `json:"success"`
		Data    struct {
			FlowToken string `json:"flow_token"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
	require.True(t, body.Success)
	require.NotEmpty(t, body.Data.FlowToken)
	return body.Data.FlowToken
}

func linuxDOSignupCallback(state string, identity service.AuthIdentity) *httptest.ResponseRecorder {
	return securityEnrollmentRequest("GET", "/api/oauth/linuxdo?state="+state+"&code=synthetic-code", "", "", identity, func(c *gin.Context) {
		c.Params = gin.Params{{Key: "provider", Value: "linuxdo"}}
		HandleOAuth(c)
	})
}

// Assert both the balance and its immutable accounting identity, including the
// restricted account. A correct total alone would miss double grants/refunds.
func assertLinuxDOSignupGrant(t *testing.T, userID int) {
	t.Helper()
	var user model.User
	require.NoError(t, model.DB.First(&user, userID).Error)
	assert.Equal(t, 1500000, user.Quota)
	assert.Equal(t, 1500000, user.QuotaBonus)
	assert.Zero(t, user.QuotaPaid)
	assert.Zero(t, user.QuotaReward)
	var rows []model.BalanceLedger
	require.NoError(t, model.DB.Find(&rows).Error)
	require.Len(t, rows, 1)
	assert.Equal(t, userID, rows[0].UserId)
	assert.Equal(t, "bonus", rows[0].Account)
	assert.Equal(t, "signup_bonus", rows[0].Reason)
	assert.Equal(t, 1500000, rows[0].Delta)
	assert.Zero(t, rows[0].BalanceBefore)
	assert.Equal(t, 1500000, rows[0].BalanceAfter)
	assert.Equal(t, "credit:linuxdo_signup:77101", rows[0].IdempotencyKey)
	var operations int64
	require.NoError(t, model.DB.Model(&model.BalanceLedgerOperation{}).Count(&operations).Error)
	assert.EqualValues(t, 1, operations)
}

func TestLinuxDOSignupFirstLoginGrantsOnceAndReplayDoesNot(t *testing.T) {
	setupLinuxDOSignupTest(t)
	common.SignupBonusEnabled = false
	identity := service.AuthIdentity{}
	state := linuxDOSignupState(t, "login", "", identity)
	response := linuxDOSignupCallback(state, identity)
	var body struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
	require.True(t, body.Success)
	var user model.User
	require.NoError(t, model.DB.Where("linux_do_id = ?", "77101").First(&user).Error)
	assertLinuxDOSignupGrant(t, user.Id)
	assert.Equal(t, http.StatusForbidden, linuxDOSignupCallback(state, identity).Code)
	response = linuxDOSignupCallback(linuxDOSignupState(t, "login", "", identity), identity)
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
	assert.True(t, body.Success)
	assertLinuxDOSignupGrant(t, user.Id)
}

func TestLinuxDOSignupExistingAccountBindingAndLoginGrantNothing(t *testing.T) {
	user, identity := setupLinuxDOSignupTest(t)
	proof := issueSecurityEnrollmentProof(t, identity, service.VerificationOperation{Scope: service.VerificationScopeAccountBind, Context: []byte(`{"provider":"linuxdo"}`)}, "password")
	state := linuxDOSignupState(t, "bind", proof, identity)
	// A different session cannot turn a binding callback into registration.
	assert.Equal(t, http.StatusForbidden, linuxDOSignupCallback(state, service.AuthIdentity{}).Code)
	response := linuxDOSignupCallback(state, identity)
	var body struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
	require.True(t, body.Success)
	assert.Equal(t, http.StatusForbidden, linuxDOSignupCallback(state, identity).Code)
	response = linuxDOSignupCallback(linuxDOSignupState(t, "login", "", service.AuthIdentity{}), service.AuthIdentity{})
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
	assert.True(t, body.Success)
	var stored model.User
	require.NoError(t, model.DB.First(&stored, user.Id).Error)
	assert.Equal(t, "77101", stored.LinuxDOId)
	assert.Zero(t, stored.Quota)
	var count int64
	require.NoError(t, model.DB.Model(&model.BalanceLedger{}).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, model.DB.Model(&model.User{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestLinuxDOSignupRejectedFlowsCreateNeitherUserNorReward(t *testing.T) {
	for _, scenario := range []string{"expired", "wrong provider", "disabled", "registration closed", "low trust", "token failure"} {
		t.Run(scenario, func(t *testing.T) {
			setupLinuxDOSignupTest(t)
			state := linuxDOSignupState(t, "login", "", service.AuthIdentity{})
			switch scenario {
			case "expired":
				require.NoError(t, model.DB.Model(&model.AuthFlow{}).Where("purpose = ?", model.AuthFlowPurposeOAuth).Update("expires_at", time.Now().Add(-time.Minute)).Error)
			case "wrong provider":
				require.NoError(t, model.DB.Model(&model.AuthFlow{}).Where("purpose = ?", model.AuthFlowPurposeOAuth).Update("provider", "github").Error)
			case "disabled":
				common.LinuxDOOAuthEnabled = false
			case "registration closed":
				common.RegisterEnabled = false
			case "low trust":
				common.LinuxDOMinimumTrustLevel = 3
			case "token failure":
				t.Setenv("LINUX_DO_TOKEN_ENDPOINT", "http://127.0.0.1:1/token")
			}
			response := linuxDOSignupCallback(state, service.AuthIdentity{})
			var body struct {
				Success bool `json:"success"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
			assert.False(t, body.Success)
			var count int64
			require.NoError(t, model.DB.Model(&model.User{}).Count(&count).Error)
			assert.EqualValues(t, 1, count)
			require.NoError(t, model.DB.Model(&model.BalanceLedger{}).Count(&count).Error)
			assert.Zero(t, count)
		})
	}
}

func TestLinuxDOSignupOtherRegistrationPathsIgnoreLegacyBonus(t *testing.T) {
	setupLinuxDOSignupTest(t)
	ordinary := &model.User{Username: "ordinary-user", LinuxDOId: "injected", Quota: 999, QuotaBonus: 999}
	require.NoError(t, ordinary.Insert(0))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/api/oauth/discord", nil)
	discord, _, err := findOrCreateOAuthUser(c, &oauth.DiscordProvider{}, &oauth.OAuthUser{ProviderUserID: "77102", Username: "discord-new-user"}, &oauth.OAuthToken{}, "")
	require.NoError(t, err)
	assert.Zero(t, ordinary.Quota)
	assert.Zero(t, discord.Quota)
	var count int64
	require.NoError(t, model.DB.Model(&model.BalanceLedgerOperation{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestLinuxDOSignupRollbackAndRetryAreAtomic(t *testing.T) {
	setupLinuxDOSignupTest(t)
	failure := errors.New("binding persistence failed")
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		user := &model.User{Username: "rollback-linuxdo"}
		if err := user.InsertLinuxDOWithTx(tx, 0, "77101"); err != nil {
			return err
		}
		return failure
	})
	require.ErrorIs(t, err, failure)
	var count int64
	for _, table := range []any{&model.BalanceLedger{}, &model.BalanceLedgerOperation{}, &model.ExternalIdentityClaim{}} {
		require.NoError(t, model.DB.Model(table).Count(&count).Error)
		assert.Zero(t, count)
	}
	require.NoError(t, model.DB.Model(&model.User{}).Where("linux_do_id = ?", "77101").Count(&count).Error)
	assert.Zero(t, count)
	user := &model.User{Username: "rollback-linuxdo"}
	require.NoError(t, model.DB.Transaction(func(tx *gorm.DB) error { return user.InsertLinuxDOWithTx(tx, 0, "77101") }))
	assertLinuxDOSignupGrant(t, user.Id)
	// Even unlinking/deleting the awarded account cannot reset the reward identity.
	require.NoError(t, user.ClearBinding("linuxdo"))
	other := &model.User{Username: "second-linuxdo-account"}
	require.Error(t, model.DB.Transaction(func(tx *gorm.DB) error { return other.InsertLinuxDOWithTx(tx, 0, "77101") }))
	require.NoError(t, model.DB.Model(&model.User{}).Where("username = ?", other.Username).Count(&count).Error)
	assert.Zero(t, count)
	assertLinuxDOSignupGrant(t, user.Id)
}

func TestLinuxDOSignupConcurrentCreationGrantsOneAccount(t *testing.T) {
	setupLinuxDOSignupTest(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Go(func() {
			<-start
			user := &model.User{Username: fmt.Sprintf("concurrent-linuxdo-%d", i)}
			results <- model.DB.Transaction(func(tx *gorm.DB) error { return user.InsertLinuxDOWithTx(tx, 0, "77101") })
		})
	}
	close(start)
	wg.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		}
	}
	assert.Equal(t, 1, succeeded)
	var users []model.User
	require.NoError(t, model.DB.Where("linux_do_id = ?", "77101").Find(&users).Error)
	require.Len(t, users, 1)
	assertLinuxDOSignupGrant(t, users[0].Id)
}

func TestLinuxDOSignupLegacyUserNeverReceivesBackfill(t *testing.T) {
	user, _ := setupLinuxDOSignupTest(t)
	// Represents a pre-policy account without an identity claim or reward row.
	require.NoError(t, model.DB.Model(user).Update("linux_do_id", "77101").Error)
	response := linuxDOSignupCallback(linuxDOSignupState(t, "login", "", service.AuthIdentity{}), service.AuthIdentity{})
	var body struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
	require.True(t, body.Success)
	var stored model.User
	require.NoError(t, model.DB.First(&stored, user.Id).Error)
	assert.Zero(t, stored.Quota)
	var count int64
	require.NoError(t, model.DB.Model(&model.BalanceLedgerOperation{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestLinuxDOSignupExistingFiveDollarGrantIsPreserved(t *testing.T) {
	user, _ := setupLinuxDOSignupTest(t)
	require.NoError(t, model.DB.Model(user).Update("linux_do_id", "77101").Error)
	_, err := model.CreditQuota(model.LedgerEntry{
		UserId:        user.Id,
		Account:       model.AccountBonus,
		Delta:         2500000,
		Reason:        model.LedgerReasonSignupBonus,
		ReferenceType: "linuxdo_signup",
		ReferenceId:   "77101",
	})
	require.NoError(t, err)
	var before []model.BalanceLedger
	require.NoError(t, model.DB.Find(&before).Error)
	require.Len(t, before, 1)
	response := linuxDOSignupCallback(linuxDOSignupState(t, "login", "", service.AuthIdentity{}), service.AuthIdentity{})
	var body struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
	require.True(t, body.Success)
	var stored model.User
	require.NoError(t, model.DB.First(&stored, user.Id).Error)
	assert.Equal(t, 2500000, stored.Quota)
	assert.Equal(t, 2500000, stored.QuotaBonus)
	var after []model.BalanceLedger
	require.NoError(t, model.DB.Find(&after).Error)
	assert.Equal(t, before, after)
}

func TestLinuxDOSignupConcurrentOldAccountBindingKeepsOneOwner(t *testing.T) {
	old, identity := setupLinuxDOSignupTest(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Go(func() {
		<-start
		results <- model.DB.Transaction(func(tx *gorm.DB) error {
			return model.UpdateUserBindColumnForSessionWithTx(tx, identity, "linux_do_id", "77101")
		})
	})
	wg.Go(func() {
		<-start
		newUser := &model.User{Username: "concurrent-new-account"}
		results <- model.DB.Transaction(func(tx *gorm.DB) error {
			return newUser.InsertLinuxDOWithTx(tx, 0, "77101")
		})
	})
	close(start)
	wg.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		}
	}
	assert.Equal(t, 1, succeeded)
	var owners []model.User
	require.NoError(t, model.DB.Where("linux_do_id = ?", "77101").Find(&owners).Error)
	require.Len(t, owners, 1)
	var claims []model.ExternalIdentityClaim
	require.NoError(t, model.DB.Where("provider = ?", "linuxdo").Find(&claims).Error)
	require.Len(t, claims, 1)
	assert.Equal(t, owners[0].Id, claims[0].UserId)
	if owners[0].Id == old.Id {
		assert.Zero(t, owners[0].Quota)
		var count int64
		require.NoError(t, model.DB.Model(&model.BalanceLedger{}).Count(&count).Error)
		assert.Zero(t, count)
	} else {
		assertLinuxDOSignupGrant(t, owners[0].Id)
	}
}
