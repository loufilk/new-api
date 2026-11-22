package controller

import (
	"os"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGitHubLoginRejectsAmbiguousHistoricalOwnership(t *testing.T) {
	setupSecurityEnrollmentTest(t)
	for _, name := range []string{"first-owner", "second-owner"} {
		require.NoError(t, model.DB.Create(&model.User{Username: name, AffCode: name, GitHubId: "900001", AuthVersion: 1, Status: common.UserStatusEnabled, Role: common.RoleCommonUser, Group: "default"}).Error)
	}
	provider := &legacyGitHubOAuthProvider{providerUserID: "900001"}
	response := legacyGitHubOAuthLogin(t, provider)
	var result securityEnrollmentResponse
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
	assert.False(t, result.Success)
	assert.Empty(t, response.Header().Values("Set-Cookie"))
	assert.Zero(t, provider.verifiedEmailCalls)
}

func TestGitHubBindingConcurrentPostgres(t *testing.T) {
	if os.Getenv("TEST_SECURITY_DIALECT") != "postgres" {
		t.Skip("requires TEST_SECURITY_DIALECT=postgres and isolated TEST_POSTGRES_DSN")
	}
	setupSecurityEnrollmentTest(t)
	users := []model.User{
		{Username: "github-owner-a", Status: common.UserStatusEnabled, AuthVersion: 1},
		{Username: "github-owner-b", Status: common.UserStatusEnabled, AuthVersion: 1},
	}
	for i := range users {
		users[i].AffCode = users[i].Username
		require.NoError(t, model.DB.Create(&users[i]).Error)
		require.NoError(t, model.DB.Transaction(func(tx *gorm.DB) error {
			return model.UpdateGitHubBindingWithTx(tx, users[i].Id, users[i].Username)
		}))
	}
	start := make(chan struct{})
	var workers sync.WaitGroup
	errs := make([]error, len(users))
	for i := range users {
		workers.Go(func() {
			<-start
			errs[i] = model.DB.Transaction(func(tx *gorm.DB) error {
				return model.UpdateGitHubBindingWithTx(tx, users[i].Id, "900001")
			})
		})
	}
	close(start)
	workers.Wait()
	winners := 0
	for i, err := range errs {
		want := users[i].Username
		if err == nil {
			winners++
			want = "900001"
		} else {
			require.ErrorIs(t, err, model.ErrExternalIdentityAlreadyClaimed)
		}
		var stored model.User
		require.NoError(t, model.DB.First(&stored, users[i].Id).Error)
		assert.Equal(t, want, stored.GitHubId)
		var claim model.ExternalIdentityClaim
		require.NoError(t, model.DB.Where("provider = ? AND user_id = ?", "github", stored.Id).First(&claim).Error)
		assert.Equal(t, want, claim.Subject, "a conflict restores the previous ownership claim")
		require.NoError(t, model.DB.Transaction(func(tx *gorm.DB) error {
			return model.UpdateGitHubBindingWithTx(tx, stored.Id, want)
		}))
	}
	assert.Equal(t, 1, winners)
}
