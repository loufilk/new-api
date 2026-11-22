package model

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupGoogleIdentityPostgres(t *testing.T) {
	t.Helper()
	if os.Getenv("TEST_MODEL_POSTGRES_DSN") == "" {
		t.Skip("requires isolated PostgreSQL 18.6 TEST_MODEL_POSTGRES_DSN")
	}
	truncateTables(t)
}

func createGoogleIdentityUser(t *testing.T, name string) *User {
	t.Helper()
	user := &User{Username: name, AffCode: name, Status: common.UserStatusEnabled, Role: common.RoleCommonUser, AuthVersion: 1}
	require.NoError(t, DB.Create(user).Error)
	return user
}

func createGoogleIdentitySession(t *testing.T, user *User) AuthSessionIdentity {
	t.Helper()
	session := newTestUserSession("google-"+user.Username, user.Id, time.Now().Unix())
	require.NoError(t, DB.Create(session).Error)
	return AuthSessionIdentity{UserID: user.Id, SessionID: session.SID, UserAuthVersion: user.AuthVersion, SessionVersion: session.Version}
}

func TestGoogleIdentityPreservesLongCaseSensitiveSubjectAndNeverMatchesEmail(t *testing.T) {
	setupGoogleIdentityPostgres(t)
	first := createGoogleIdentityUser(t, "google-owner")
	second := createGoogleIdentityUser(t, "google-case")
	subject := strings.Repeat("A", 255)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return UpdateGoogleBindingWithTx(tx, first.Id, subject) }))
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return UpdateGoogleBindingWithTx(tx, second.Id, strings.ToLower(subject)) }))
	stored, err := GetUserByGoogleID(subject)
	require.NoError(t, err)
	assert.Equal(t, first.Id, stored.Id)
	assert.Equal(t, subject, stored.GoogleId)
	assert.True(t, IsGoogleIDAlreadyTaken(subject))
	assert.False(t, IsGoogleIDAlreadyTaken("different-subject"))
	profile, err := GetSelfUserById(first.Id)
	require.NoError(t, err)
	assert.Equal(t, subject, profile.GoogleId)
	var claim ExternalIdentityClaim
	require.NoError(t, DB.Where("provider = ? AND user_id = ?", ExternalIdentityProviderGoogle, first.Id).First(&claim).Error)
	assert.Len(t, claim.Subject, 64)
	assert.NotContains(t, claim.Subject, subject)

	require.NoError(t, DB.Model(first).Update("email", "same@example.invalid").Error)
	_, err = GetUserByGoogleID("same@example.invalid")
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestGoogleIdentityConcurrentClaimsKeepOneOwnerAndRollbackLoser(t *testing.T) {
	setupGoogleIdentityPostgres(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, name := range []string{"google-race-one", "google-race-two"} {
		wg.Go(func() {
			<-start
			results <- DB.Transaction(func(tx *gorm.DB) error {
				user := User{Username: name, AffCode: name, Status: common.UserStatusEnabled, Role: common.RoleCommonUser}
				if err := tx.Create(&user).Error; err != nil {
					return err
				}
				return UpdateGoogleBindingWithTx(tx, user.Id, "same-google-subject")
			})
		})
	}
	close(start)
	wg.Wait()
	close(results)
	var succeeded, conflicted int
	for err := range results {
		if err == nil {
			succeeded++
		} else {
			require.ErrorIs(t, err, ErrExternalIdentityAlreadyClaimed)
			conflicted++
		}
	}
	assert.Equal(t, 1, succeeded)
	assert.Equal(t, 1, conflicted)
	var users int64
	require.NoError(t, DB.Model(&User{}).Where("username IN ?", []string{"google-race-one", "google-race-two"}).Count(&users).Error)
	assert.EqualValues(t, 1, users)
	owner, err := GetUserByGoogleID("same-google-subject")
	require.NoError(t, err)
	assert.Equal(t, common.RoleCommonUser, owner.Role)
}

func TestGoogleIdentityRejectsReplacementAndConcurrentSecondBinding(t *testing.T) {
	setupGoogleIdentityPostgres(t)
	user := createGoogleIdentityUser(t, "google-slot")
	identity := createGoogleIdentitySession(t, user)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, subject := range []string{"first-google-subject", "second-google-subject"} {
		wg.Go(func() {
			<-start
			results <- DB.Transaction(func(tx *gorm.DB) error { return BindGoogleForSessionWithTx(tx, identity, subject) })
		})
	}
	close(start)
	wg.Wait()
	close(results)
	var succeeded, conflicted int
	for err := range results {
		if err == nil {
			succeeded++
		} else {
			require.ErrorIs(t, err, ErrExternalIdentityAlreadyClaimed)
			conflicted++
		}
	}
	assert.Equal(t, 1, succeeded)
	assert.Equal(t, 1, conflicted)
	require.NoError(t, DB.First(user, user.Id).Error)
	err := DB.Transaction(func(tx *gorm.DB) error { return UpdateGoogleBindingWithTx(tx, user.Id, "replacement-subject") })
	assert.ErrorIs(t, err, ErrExternalIdentityAlreadyClaimed)
	assert.True(t, IsGoogleIDAlreadyTaken(user.GoogleId))
	assert.False(t, IsGoogleIDAlreadyTaken("replacement-subject"))
}

func TestGoogleIdentityRefusesMalformedSubjectWithoutWriting(t *testing.T) {
	setupGoogleIdentityPostgres(t)
	user := createGoogleIdentityUser(t, "google-invalid")
	identity := createGoogleIdentitySession(t, user)
	for _, subject := range []string{"", " padded ", "new\nline", "非ASCII", strings.Repeat("A", 256)} {
		err := DB.Transaction(func(tx *gorm.DB) error { return BindGoogleForSessionWithTx(tx, identity, subject) })
		assert.ErrorIs(t, err, ErrAccountBindingChanged)
	}
	var count int64
	require.NoError(t, DB.Model(&ExternalIdentityClaim{}).Where("provider = ?", ExternalIdentityProviderGoogle).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, DB.First(user, user.Id).Error)
	assert.Empty(t, user.GoogleId)
}

func TestGoogleIdentitySoftDeletionAndBanCannotReassignIdentity(t *testing.T) {
	setupGoogleIdentityPostgres(t)
	owner := createGoogleIdentityUser(t, "google-disabled")
	other := createGoogleIdentityUser(t, "google-other")
	identity := createGoogleIdentitySession(t, owner)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return UpdateGoogleBindingWithTx(tx, owner.Id, "reserved-subject") }))
	require.NoError(t, DB.Model(owner).Update("status", common.UserStatusDisabled).Error)
	err := DB.Transaction(func(tx *gorm.DB) error { return BindGoogleForSessionWithTx(tx, identity, "new-subject") })
	assert.ErrorIs(t, err, ErrUserSessionInactive)
	stored, err := GetUserByGoogleID("reserved-subject")
	require.NoError(t, err)
	assert.Equal(t, common.UserStatusDisabled, stored.Status)
	require.NoError(t, DB.Delete(owner).Error)
	assert.True(t, IsGoogleIDAlreadyTaken("reserved-subject"))
	_, err = GetUserByGoogleID("reserved-subject")
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	err = DB.Transaction(func(tx *gorm.DB) error { return UpdateGoogleBindingWithTx(tx, other.Id, "reserved-subject") })
	assert.ErrorIs(t, err, ErrExternalIdentityAlreadyClaimed)
}

func TestGoogleIdentityUnbindRequiresCurrentSessionAndUsableAlternative(t *testing.T) {
	setupGoogleIdentityPostgres(t)
	user := createGoogleIdentityUser(t, "google-unbind")
	identity := createGoogleIdentitySession(t, user)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return BindGoogleForSessionWithTx(tx, identity, "unlink-subject") }))
	googleOnly := AccountLoginMethods{OAuthColumns: []string{"google_id"}}
	assert.ErrorIs(t, UnbindGoogleForSession(identity, googleOnly), ErrLastLoginMethod)
	require.NoError(t, DB.Model(user).Update("password", "password-hash").Error)
	assert.ErrorIs(t, UnbindGoogleForSession(identity, googleOnly), ErrLastLoginMethod)
	stale := identity
	stale.SessionVersion++
	assert.ErrorIs(t, UnbindGoogleForSession(stale, AccountLoginMethods{Password: true}), ErrUserSessionInactive)
	assert.True(t, IsGoogleIDAlreadyTaken("unlink-subject"))
	require.NoError(t, UnbindGoogleForSession(identity, AccountLoginMethods{Password: true}))
	assert.False(t, IsGoogleIDAlreadyTaken("unlink-subject"))
	require.NoError(t, DB.First(user, user.Id).Error)
	assert.Empty(t, user.GoogleId)
	assert.ErrorIs(t, UnbindGoogleForSession(identity, AccountLoginMethods{Password: true}), ErrUserSessionInactive)
}

func TestGoogleIdentityUnbindInvalidatesPendingLoginAndOldSession(t *testing.T) {
	setupGoogleIdentityPostgres(t)
	user := createGoogleIdentityUser(t, "google-pending")
	identity := createGoogleIdentitySession(t, user)
	require.NoError(t, DB.Model(user).Update("password", "password-hash").Error)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return BindGoogleForSessionWithTx(tx, identity, "pending-subject") }))
	flowToken, _, err := CreateAuthFlow(AuthFlowCreate{
		Purpose: AuthFlowPurposeLoginVerification, UserId: user.Id,
		Payload: `{"auth_version":1,"login_method":"google"}`, ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	pendingSession := newTestUserSession("pending-google-login", user.Id, time.Now().Unix())
	pendingSession.LoginMethod = "google"
	require.NoError(t, UnbindGoogleForSession(identity, AccountLoginMethods{Password: true}))
	current, err := GetUserCache(user.Id)
	require.NoError(t, err)
	assert.EqualValues(t, 2, current.AuthVersion)
	err = DB.Transaction(func(tx *gorm.DB) error { return ValidateAuthSessionWithTx(tx, identity) })
	assert.ErrorIs(t, err, ErrUserSessionInactive)
	verified := false
	err = CreateUserSessionFromLoginFlow(flowToken, pendingSession, func(*gorm.DB, *AuthFlow, *UserVerificationState) error {
		verified = true
		return nil
	})
	assert.ErrorIs(t, err, ErrUserSessionInactive)
	assert.False(t, verified)
	var issued int64
	require.NoError(t, DB.Model(&UserSession{}).Where("sid = ?", pendingSession.SID).Count(&issued).Error)
	assert.Zero(t, issued)
}

func TestGoogleIdentityAdminClearRechecksPrivilegeAndSession(t *testing.T) {
	for _, scenario := range []string{"actor demoted", "actor session revoked", "target promoted", "target version changed", "target binding changed"} {
		t.Run(scenario, func(t *testing.T) {
			setupGoogleIdentityPostgres(t)
			actor := createGoogleIdentityUser(t, "google-actor")
			require.NoError(t, DB.Model(actor).Update("role", common.RoleAdminUser).Error)
			identity := createGoogleIdentitySession(t, actor)
			target := createGoogleIdentityUser(t, "google-target")
			require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return UpdateGoogleBindingWithTx(tx, target.Id, "guarded-subject") }))
			require.NoError(t, DB.First(target, target.Id).Error)
			expected := *target
			switch scenario {
			case "actor demoted":
				require.NoError(t, DB.Model(actor).Update("role", common.RoleCommonUser).Error)
			case "actor session revoked":
				require.NoError(t, DB.Model(&UserSession{}).Where("sid = ?", identity.SessionID).Update("status", UserSessionStatusRevoked).Error)
			case "target promoted":
				require.NoError(t, DB.Model(target).Update("role", common.RoleAdminUser).Error)
			case "target version changed":
				require.NoError(t, DB.Model(target).Update("auth_version", target.AuthVersion+1).Error)
			case "target binding changed":
				expected.GoogleId = "older-binding"
			}
			require.Error(t, ClearGoogleBindingByAdministrator(identity, expected))
			stored, err := GetUserByGoogleID("guarded-subject")
			require.NoError(t, err)
			assert.Equal(t, target.Id, stored.Id)
			assert.True(t, IsGoogleIDAlreadyTaken("guarded-subject"))
		})
	}
}

func TestGoogleIdentityAdminClearInvalidatesTargetLoginAuthorization(t *testing.T) {
	setupGoogleIdentityPostgres(t)
	actor := createGoogleIdentityUser(t, "google-clear-actor")
	require.NoError(t, DB.Model(actor).Update("role", common.RoleAdminUser).Error)
	identity := createGoogleIdentitySession(t, actor)
	target := createGoogleIdentityUser(t, "google-clear-target")
	targetIdentity := createGoogleIdentitySession(t, target)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return UpdateGoogleBindingWithTx(tx, target.Id, "admin-clear-subject") }))
	require.NoError(t, DB.First(target, target.Id).Error)
	require.NoError(t, ClearGoogleBindingByAdministrator(identity, *target))
	current, err := GetUserCache(target.Id)
	require.NoError(t, err)
	assert.EqualValues(t, 2, current.AuthVersion)
	assert.Equal(t, common.RoleCommonUser, current.Role)
	err = DB.Transaction(func(tx *gorm.DB) error { return ValidateAuthSessionWithTx(tx, targetIdentity) })
	assert.ErrorIs(t, err, ErrUserSessionInactive)
	assert.False(t, IsGoogleIDAlreadyTaken("admin-clear-subject"))
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return ValidateAuthSessionWithTx(tx, identity) }))
}

func TestGoogleIdentityCountsAsAlternativeWhenUnbindingCustomProvider(t *testing.T) {
	setupGoogleIdentityPostgres(t)
	user := createGoogleIdentityUser(t, "google-fallback")
	identity := createGoogleIdentitySession(t, user)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return BindGoogleForSessionWithTx(tx, identity, "fallback-subject") }))
	binding := UserOAuthBinding{UserId: user.Id, ProviderId: 73, ProviderUserId: "custom-subject"}
	require.NoError(t, DB.Create(&binding).Error)
	assert.ErrorIs(t, UnbindUserOAuthForSession(identity, binding.ProviderId, AccountLoginMethods{}), ErrLastLoginMethod)
	require.NoError(t, UnbindUserOAuthForSession(identity, binding.ProviderId, AccountLoginMethods{OAuthColumns: []string{"google_id"}}))
	assert.True(t, IsGoogleIDAlreadyTaken("fallback-subject"))
}

func TestGoogleIdentityAdministratorClearReleasesClaimWithoutChangingAccount(t *testing.T) {
	setupGoogleIdentityPostgres(t)
	user := createGoogleIdentityUser(t, "google-admin-clear")
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return UpdateGoogleBindingWithTx(tx, user.Id, "clear-subject") }))
	require.NoError(t, DB.Model(user).Updates(map[string]any{"role": common.RoleAdminUser, "status": common.UserStatusDisabled, "group": "preserved-group"}).Error)
	// The caller's snapshot predates the status/role changes above.
	require.NoError(t, user.ClearBinding("google"))
	assert.Empty(t, user.GoogleId)
	assert.Equal(t, common.RoleAdminUser, user.Role)
	assert.Equal(t, common.UserStatusDisabled, user.Status)
	assert.Equal(t, "preserved-group", user.Group)
	assert.False(t, IsGoogleIDAlreadyTaken("clear-subject"))
}

func TestGoogleIdentityHardDeletionPreservesExistingReleasePolicy(t *testing.T) {
	setupGoogleIdentityPostgres(t)
	user := createGoogleIdentityUser(t, "google-hard-delete")
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return UpdateGoogleBindingWithTx(tx, user.Id, "hard-delete-subject") }))
	_, err := user.HardDelete()
	require.NoError(t, err)
	assert.False(t, IsGoogleIDAlreadyTaken("hard-delete-subject"))
	other := createGoogleIdentityUser(t, "google-after-delete")
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return UpdateGoogleBindingWithTx(tx, other.Id, "hard-delete-subject") }))
	stored, err := GetUserByGoogleID("hard-delete-subject")
	require.NoError(t, err)
	assert.Equal(t, other.Id, stored.Id)
}

func TestGoogleIdentityStartupBackfillIsIdempotentAndRejectsAmbiguity(t *testing.T) {
	setupGoogleIdentityPostgres(t)
	user := createGoogleIdentityUser(t, "google-restore")
	require.NoError(t, DB.Model(user).Update("google_id", "restored-subject").Error)
	require.NoError(t, DB.AutoMigrate(&User{}, &ExternalIdentityClaim{}))
	require.NoError(t, InitializeExternalIdentityClaims())
	require.NoError(t, DB.AutoMigrate(&User{}, &ExternalIdentityClaim{}))
	require.NoError(t, InitializeExternalIdentityClaims())
	stored, err := GetUserByGoogleID("restored-subject")
	require.NoError(t, err)
	assert.Equal(t, user.Id, stored.Id)
	duplicate := createGoogleIdentityUser(t, "google-duplicate")
	require.NoError(t, DB.Model(duplicate).Update("google_id", "restored-subject").Error)
	err = InitializeExternalIdentityClaims()
	assert.ErrorIs(t, err, ErrExternalIdentityAlreadyClaimed)
	stored, err = GetUserByGoogleID("restored-subject")
	require.NoError(t, err)
	assert.Equal(t, user.Id, stored.Id)
}

func TestGoogleIdentityStartupRejectsDuplicateRestoredSubjectsAtomically(t *testing.T) {
	setupGoogleIdentityPostgres(t)
	first := createGoogleIdentityUser(t, "google-restore-one")
	second := createGoogleIdentityUser(t, "google-restore-two")
	require.NoError(t, DB.Model(&User{}).Where("id IN ?", []int{first.Id, second.Id}).Update("google_id", "duplicate-restored-subject").Error)
	assert.ErrorIs(t, InitializeExternalIdentityClaims(), ErrExternalIdentityAlreadyClaimed)
	var count int64
	require.NoError(t, DB.Model(&ExternalIdentityClaim{}).Where("provider = ?", ExternalIdentityProviderGoogle).Count(&count).Error)
	assert.Zero(t, count)
}

func TestGoogleIdentityCustomProviderSlugIsReservedCaseInsensitively(t *testing.T) {
	for _, slug := range []string{"google", "Google", "GOOGLE"} {
		t.Run(slug, func(t *testing.T) {
			provider := CustomOAuthProvider{Name: "Unrelated display name", Slug: slug}
			err := validateCustomOAuthProvider(&provider)
			require.ErrorContains(t, err, "google OAuth provider name is reserved")
		})
	}
}
