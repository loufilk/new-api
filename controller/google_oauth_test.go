package controller

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"maps"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/oauth"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type googleTestTransport struct{ target *url.URL }

func (transport googleTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Host != "oauth2.googleapis.com" && request.URL.Host != "www.googleapis.com" {
		return nil, fmt.Errorf("unexpected OAuth host: %s", request.URL.Host)
	}
	clone := request.Clone(request.Context())
	clone.URL.Scheme, clone.URL.Host = transport.target.Scheme, transport.target.Host
	return http.DefaultTransport.RoundTrip(clone)
}

type googleTestGrant struct {
	challenge string
	claims    jwt.MapClaims
	key       *rsa.PrivateKey
}

type googleOAuthFixture struct {
	user           *model.User
	identity       service.AuthIdentity
	client         *http.Client
	key            *rsa.PrivateKey
	mutex          sync.Mutex
	grants         map[string]googleTestGrant
	cookies        map[string]*http.Cookie
	exchanges      int
	tokenStatus    int
	jwksStatus     int
	jwksOversized  bool
	tokenOversized bool
}

func setupGoogleOAuthTest(t *testing.T) *googleOAuthFixture {
	t.Helper()
	require.Equal(t, "postgres", os.Getenv("TEST_SECURITY_DIALECT"), "Google tests require explicit disposable PostgreSQL")
	user, identity := setupSecurityEnrollmentTest(t)
	require.NoError(t, model.LOG_DB.AutoMigrate(&model.Log{}))
	previousRegister, previousPassword := common.RegisterEnabled, common.PasswordLoginEnabled
	common.RegisterEnabled, common.PasswordLoginEnabled = true, true
	t.Cleanup(func() { common.RegisterEnabled, common.PasswordLoginEnabled = previousRegister, previousPassword })
	require.NoError(t, model.DB.AutoMigrate(&model.ExternalIdentityClaim{}, &model.Option{}))
	previousEnabled := common.GoogleOAuthEnabled
	previousSettings := *system_setting.GetGoogleSettings()
	previousAddress := system_setting.ServerAddress
	previousSecure, previousTrusted := common.SessionCookieSecure, common.SessionCookieTrustedURLs
	common.SessionCookieSecure, common.SessionCookieTrustedURLs = true, []string{"https://example.com"}
	previousProvider := oauth.GetProvider("google")
	common.OptionMapRWMutex.Lock()
	previousOptions := common.OptionMap
	common.OptionMap = make(map[string]string)
	maps.Copy(common.OptionMap, previousOptions)
	common.OptionMapRWMutex.Unlock()
	common.GoogleOAuthEnabled = true
	*system_setting.GetGoogleSettings() = system_setting.GoogleSettings{ClientID: "google-test-client", ClientSecret: "google-client-secret"}
	system_setting.ServerAddress = "https://example.com"
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
		common.GoogleOAuthEnabled = previousEnabled
		*system_setting.GetGoogleSettings() = previousSettings
		system_setting.ServerAddress = previousAddress
		common.SessionCookieSecure, common.SessionCookieTrustedURLs = previousSecure, previousTrusted
		oauth.Register("google", previousProvider)
		oauth.UnregisterCustomProvider("google")
	})
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	fixture := &googleOAuthFixture{user: user, identity: identity, key: key, grants: map[string]googleTestGrant{}, cookies: map[string]*http.Cookie{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		fixture.mutex.Lock()
		defer fixture.mutex.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/oauth2/v3/certs":
			if fixture.jwksStatus != 0 {
				w.WriteHeader(fixture.jwksStatus)
				return
			}
			padding := ""
			if fixture.jwksOversized {
				padding = strings.Repeat("x", 1<<20)
			}
			payload, err := common.Marshal(map[string]any{"padding": padding, "keys": []any{map[string]any{
				"kty": "RSA", "kid": "google-test-key", "alg": "RS256", "use": "sig",
				"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
			}}})
			if assert.NoError(t, err) {
				_, _ = w.Write(payload)
			}
		case "/token":
			fixture.exchanges++
			if fixture.tokenStatus != 0 {
				w.WriteHeader(fixture.tokenStatus)
				return
			}
			if request.ParseForm() != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if request.Form.Get("client_id") != "google-test-client" || request.Form.Get("client_secret") != "google-client-secret" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			code := request.Form.Get("code")
			grant, ok := fixture.grants[code]
			if !ok || request.Form.Get("grant_type") != "authorization_code" ||
				request.Form.Get("client_id") != "google-test-client" || request.Form.Get("redirect_uri") != "https://example.com/oauth/google" ||
				oauth2.S256ChallengeFromVerifier(request.Form.Get("code_verifier")) != grant.challenge {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			delete(fixture.grants, code)
			token := jwt.NewWithClaims(jwt.SigningMethodRS256, grant.claims)
			token.Header["kid"] = "google-test-key"
			signed, err := token.SignedString(grant.key)
			if !assert.NoError(t, err) {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			padding := ""
			if fixture.tokenOversized {
				padding = strings.Repeat("x", 1<<20)
			}
			payload, err := common.Marshal(map[string]any{"access_token": "test-access-token", "id_token": signed, "token_type": "Bearer", "padding": padding})
			if assert.NoError(t, err) {
				_, _ = w.Write(payload)
			}
		default:
			t.Errorf("unexpected Google endpoint: %s", request.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	require.NoError(t, err)
	fixture.client = &http.Client{Transport: googleTestTransport{target: target}, Timeout: 5 * time.Second}
	oauth.Register("google", oauth.NewGoogleProvider(fixture.client))
	return fixture
}

func (fixture *googleOAuthFixture) authorization(t *testing.T, intent string, identity service.AuthIdentity, scope string, claims jwt.MapClaims) (string, string) {
	t.Helper()
	request, err := common.Marshal(oauthStateRequest{Provider: "google", Intent: intent, Scope: scope})
	require.NoError(t, err)
	proof := ""
	if intent == "bind" {
		proof = issueSecurityEnrollmentProof(t, identity, service.VerificationOperation{Scope: service.VerificationScopeAccountBind, Context: []byte(`{"provider":"google"}`)}, service.VerificationMethodPassword)
	}
	response := securityEnrollmentRequest("POST", "/api/oauth/state", string(request), proof, identity, GenerateOAuthCode)
	var body securityEnrollmentResponse
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
	require.True(t, body.Success, response.Body.String())
	var data struct {
		State string `json:"flow_token"`
		URL   string `json:"authorization_url"`
	}
	require.NoError(t, common.Unmarshal(body.Data, &data))
	authorizationURL, err := url.Parse(data.URL)
	require.NoError(t, err)
	assert.Equal(t, "https://accounts.google.com/o/oauth2/v2/auth", authorizationURL.Scheme+"://"+authorizationURL.Host+authorizationURL.Path)
	assert.Equal(t, "openid email profile", authorizationURL.Query().Get("scope"))
	assert.Equal(t, "S256", authorizationURL.Query().Get("code_challenge_method"))
	assert.Equal(t, data.State, authorizationURL.Query().Get("state"))
	assert.Empty(t, authorizationURL.Query().Get("code_verifier"))
	assert.NotContains(t, response.Body.String(), "google-client-secret")
	require.Len(t, response.Result().Cookies(), 1)
	cookie := response.Result().Cookies()[0]
	assert.True(t, cookie.HttpOnly)
	assert.True(t, cookie.Secure)
	assert.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
	assert.Equal(t, "", cookie.Domain)
	assert.Equal(t, "/", cookie.Path)
	assert.Equal(t, 600, cookie.MaxAge)
	if _, hasNonce := claims["nonce"]; !hasNonce {
		claims["nonce"] = authorizationURL.Query().Get("nonce")
	}
	code := "code-" + data.State
	fixture.mutex.Lock()
	fixture.cookies[data.State] = cookie
	fixture.grants[code] = googleTestGrant{challenge: authorizationURL.Query().Get("code_challenge"), claims: claims, key: fixture.key}
	fixture.mutex.Unlock()
	return data.State, code
}

func googleIdentityClaims(sub string) jwt.MapClaims {
	return jwt.MapClaims{"iss": oauth.GoogleIssuer, "aud": "google-test-client", "sub": sub, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix(), "email": sub + "@example.invalid", "email_verified": true, "hd": "example.invalid", "name": "Google User"}
}

func (fixture *googleOAuthFixture) callback(state, code string, identity service.AuthIdentity) *httptest.ResponseRecorder {
	path := "/api/oauth/google?" + url.Values{"state": {state}, "code": {code}}.Encode()
	return securityEnrollmentRequest("GET", path, "", "", identity, func(c *gin.Context) {
		fixture.mutex.Lock()
		cookie := fixture.cookies[state]
		fixture.mutex.Unlock()
		if cookie != nil {
			c.Request.AddCookie(cookie)
		}
		c.Params = gin.Params{{Key: "provider", Value: "google"}}
		HandleOAuth(c)
	})
}

func TestGoogleOAuthSignupLoginAndReplay(t *testing.T) {
	f := setupGoogleOAuthTest(t)
	state, code := f.authorization(t, "login", service.AuthIdentity{}, "", googleIdentityClaims("first"))
	r := f.callback(state, code, service.AuthIdentity{})
	require.Contains(t, r.Body.String(), `"success":true`)
	user, err := model.GetUserByGoogleID("first")
	require.NoError(t, err)
	assert.Equal(t, common.RoleCommonUser, user.Role)
	assert.Equal(t, "first@example.invalid", user.Email)
	assert.Empty(t, user.Password)
	assert.Empty(t, user.GitHubId)
	assert.Zero(t, user.QuotaBonus)
	assert.NotContains(t, r.Body.String(), "test-access-token")
	assert.NotContains(t, r.Body.String(), "google-client-secret")
	assert.Equal(t, http.StatusForbidden, f.callback(state, code, service.AuthIdentity{}).Code)
	assert.Equal(t, 1, f.exchanges)
	common.RegisterEnabled = false
	claims := googleIdentityClaims("first")
	claims["email"] = "changed@example.invalid"
	delete(claims, "hd")
	state, code = f.authorization(t, "login", service.AuthIdentity{}, "", claims)
	r = f.callback(state, code, service.AuthIdentity{})
	require.Contains(t, r.Body.String(), `"success":true`)
	stored, err := model.GetUserByGoogleID("first")
	require.NoError(t, err)
	assert.Equal(t, user.Id, stored.Id)
	assert.Equal(t, "first@example.invalid", stored.Email, "login does not rewrite local recovery email")
	state, code = f.authorization(t, "login", service.AuthIdentity{}, "", googleIdentityClaims("new-disabled"))
	assert.Contains(t, f.callback(state, code, service.AuthIdentity{}).Body.String(), "New user registration has been disabled")
}

func TestGoogleOAuthOnlyAuthoritativeEmailBecomesRecoveryEmail(t *testing.T) {
	for _, test := range []struct {
		name      string
		email     string
		hosted    string
		wantEmail bool
	}{
		{name: "Gmail without hosted domain", email: "synthetic-google-owner@gmail.com", wantEmail: true},
		{name: "Workspace with signed hosted domain", email: "owner@workspace.example.invalid", hosted: "workspace.example.invalid", wantEmail: true},
		{name: "third-party verified email without hosted domain", email: "reassigned@external.example.invalid"},
		{name: "third-party verified email with blank hosted domain", email: "blank@external.example.invalid", hosted: " "},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := setupGoogleOAuthTest(t)
			claims := googleIdentityClaims("email-authority")
			claims["email"] = test.email
			delete(claims, "hd")
			if test.hosted != "" {
				claims["hd"] = test.hosted
			}
			state, code := fixture.authorization(t, "login", service.AuthIdentity{}, "", claims)
			response := fixture.callback(state, code, service.AuthIdentity{})
			require.Contains(t, response.Body.String(), `"success":true`)
			user, err := model.GetUserByGoogleID("email-authority")
			require.NoError(t, err)
			assert.Equal(t, common.RoleCommonUser, user.Role, "hosted domain must not grant privileges")
			assert.Equal(t, test.wantEmail, model.VerifiedSupportEmail(user), "only authoritative Google mailbox proof can enable support mail")
			if test.wantEmail {
				assert.Equal(t, test.email, user.Email)
				return
			}
			assert.Empty(t, user.Email, "Google is not authoritative for a third-party mailbox")
			// Even possession of a mailbox recovery code must not let its current
			// owner reset the distinct Google subject's newly created local account.
			common.RegisterVerificationCodeWithKey(test.email, "synthetic-recovery-code", common.PasswordResetPurpose)
			t.Cleanup(func() { common.DeleteKey(test.email, common.PasswordResetPurpose) })
			body, err := common.Marshal(PasswordResetRequest{Email: test.email, Token: "synthetic-recovery-code"})
			require.NoError(t, err)
			reset := securityEnrollmentRequest(http.MethodPost, "/api/user/reset", string(body), "", service.AuthIdentity{}, ResetPassword)
			assert.Contains(t, reset.Body.String(), `"success":false`)
			stored, err := model.GetUserByGoogleID("email-authority")
			require.NoError(t, err)
			assert.Empty(t, stored.Password)
			assert.Equal(t, user.AuthVersion, stored.AuthVersion)
			common.RegisterEnabled = false
			claims = googleIdentityClaims("email-authority")
			claims["email"] = test.email
			delete(claims, "hd")
			state, code = fixture.authorization(t, "login", service.AuthIdentity{}, "", claims)
			assert.Contains(t, fixture.callback(state, code, service.AuthIdentity{}).Body.String(), `"success":true`, "existing subject can sign in without a recovery email")
		})
	}
}

func TestGoogleOAuthThirdPartyEmailCannotClaimExistingLocalAccount(t *testing.T) {
	fixture := setupGoogleOAuthTest(t)
	email := "existing-local@external.example.invalid"
	require.NoError(t, model.DB.Model(fixture.user).Update("email", email).Error)
	claims := googleIdentityClaims("independent-google-subject")
	claims["email"] = email
	delete(claims, "hd")
	state, code := fixture.authorization(t, "login", service.AuthIdentity{}, "", claims)
	response := fixture.callback(state, code, service.AuthIdentity{})
	require.Contains(t, response.Body.String(), `"success":true`)
	googleUser, err := model.GetUserByGoogleID("independent-google-subject")
	require.NoError(t, err)
	assert.NotEqual(t, fixture.user.Id, googleUser.Id)
	assert.Empty(t, googleUser.Email)
	localUser, err := model.GetUniqueUserByEmail(email)
	require.NoError(t, err)
	assert.Equal(t, fixture.user.Id, localUser.Id)
	assert.Empty(t, localUser.GoogleId)
}

func TestGoogleOAuthNeverMergesMatchingEmail(t *testing.T) {
	f := setupGoogleOAuthTest(t)
	require.NoError(t, model.DB.Model(f.user).Update("email", "same@example.invalid").Error)
	claims := googleIdentityClaims("new-google")
	claims["email"] = "same@example.invalid"
	state, code := f.authorization(t, "login", service.AuthIdentity{}, "", claims)
	r := f.callback(state, code, service.AuthIdentity{})
	assert.Contains(t, r.Body.String(), "Email address is already in use")
	assert.False(t, model.IsGoogleIDAlreadyTaken("new-google"))
	var count int64
	require.NoError(t, model.DB.Model(&model.User{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestGoogleOAuthRejectsInvalidSignedClaims(t *testing.T) {
	f := setupGoogleOAuthTest(t)
	for _, tt := range []struct {
		name   string
		change func(jwt.MapClaims)
	}{
		{"issuer", func(c jwt.MapClaims) { c["iss"] = "https://attacker.invalid" }},
		{"audience", func(c jwt.MapClaims) { c["aud"] = "another-client" }},
		{"expired", func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Minute).Unix() }},
		{"missing expiry", func(c jwt.MapClaims) { delete(c, "exp") }},
		{"future issuance", func(c jwt.MapClaims) { c["iat"] = time.Now().Add(time.Hour).Unix() }},
		{"missing issuance", func(c jwt.MapClaims) { delete(c, "iat") }},
		{"missing subject", func(c jwt.MapClaims) { delete(c, "sub") }},
		{"oversized subject", func(c jwt.MapClaims) { c["sub"] = strings.Repeat("a", 256) }},
		{"subject whitespace", func(c jwt.MapClaims) { c["sub"] = " a" }},
		{"nonce", func(c jwt.MapClaims) { c["nonce"] = "wrong" }},
		{"unverified email", func(c jwt.MapClaims) { c["email_verified"] = false }},
		{"missing verification", func(c jwt.MapClaims) { delete(c, "email_verified") }},
		{"missing email", func(c jwt.MapClaims) { delete(c, "email") }},
		{"malformed email", func(c jwt.MapClaims) { c["email"] = "display <x@example.invalid>" }},
		{"wrong authorized party", func(c jwt.MapClaims) { c["azp"] = "another-client" }},
		{"multiple audiences without authorized party", func(c jwt.MapClaims) { c["aud"] = []string{"google-test-client", "another-client"} }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			claims := googleIdentityClaims("invalid")
			tt.change(claims)
			state, code := f.authorization(t, "login", service.AuthIdentity{}, "", claims)
			r := f.callback(state, code, service.AuthIdentity{})
			assert.Contains(t, r.Body.String(), "GOOGLE_OAUTH_FAILED")
			assert.NotContains(t, r.Body.String(), "access_token")
			assert.False(t, model.IsGoogleIDAlreadyTaken("invalid"))
			assert.Equal(t, http.StatusForbidden, f.callback(state, code, service.AuthIdentity{}).Code)
		})
	}
}

func TestGoogleOAuthRejectsSignatureAndTransportFailures(t *testing.T) {
	for _, failure := range []string{"bad signature", "token error", "jwks error", "redirect", "oversized jwks", "oversized token"} {
		t.Run(failure, func(t *testing.T) {
			f := setupGoogleOAuthTest(t)
			state, code := f.authorization(t, "login", service.AuthIdentity{}, "", googleIdentityClaims("failure"))
			switch failure {
			case "bad signature":
				key, err := rsa.GenerateKey(rand.Reader, 2048)
				require.NoError(t, err)
				grant := f.grants[code]
				grant.key = key
				f.grants[code] = grant
			case "token error":
				f.tokenStatus = http.StatusServiceUnavailable
			case "jwks error":
				f.jwksStatus = http.StatusServiceUnavailable
			case "redirect":
				f.tokenStatus = http.StatusFound
			case "oversized jwks":
				f.jwksOversized = true
			case "oversized token":
				f.tokenOversized = true
			}
			r := f.callback(state, code, service.AuthIdentity{})
			assert.Contains(t, r.Body.String(), "GOOGLE_OAUTH_FAILED")
			assert.False(t, model.IsGoogleIDAlreadyTaken("failure"))
			assert.Equal(t, http.StatusForbidden, f.callback(state, code, service.AuthIdentity{}).Code)
			assert.Equal(t, 1, f.exchanges)
		})
	}
}

func TestGoogleOAuthStateBoundToBrowserProviderExpiryAndCancellation(t *testing.T) {
	f := setupGoogleOAuthTest(t)
	state, code := f.authorization(t, "login", service.AuthIdentity{}, "", googleIdentityClaims("bound"))
	cookie := f.cookies[state]
	delete(f.cookies, state)
	assert.Equal(t, http.StatusForbidden, f.callback(state, code, service.AuthIdentity{}).Code)
	f.cookies[state] = &http.Cookie{Name: cookie.Name, Value: "different-browser"}
	assert.Equal(t, http.StatusForbidden, f.callback(state, code, service.AuthIdentity{}).Code)
	assert.Zero(t, f.exchanges)
	f.cookies[state] = cookie
	require.NoError(t, model.DB.Model(&model.AuthFlow{}).Where("provider = ?", "google").Update("expires_at", time.Now().Add(-time.Minute)).Error)
	assert.Equal(t, http.StatusForbidden, f.callback(state, code, service.AuthIdentity{}).Code)
	assert.Zero(t, f.exchanges)
	state, _ = f.authorization(t, "login", service.AuthIdentity{}, "", googleIdentityClaims("cancel"))
	response := securityEnrollmentRequest("GET", "/api/oauth/google?"+url.Values{"state": {state}, "error": {"access_denied"}, "error_description": {"sensitive attacker content"}}.Encode(), "", "", service.AuthIdentity{}, func(c *gin.Context) {
		c.Params = gin.Params{{Key: "provider", Value: "google"}}
		c.Request.AddCookie(f.cookies[state])
		HandleOAuth(c)
	})
	assert.Contains(t, response.Body.String(), "Google sign-in was cancelled")
	assert.NotContains(t, response.Body.String(), "sensitive attacker content")
	assert.Equal(t, http.StatusForbidden, f.callback(state, "anything", service.AuthIdentity{}).Code)
	assert.Zero(t, f.exchanges)
}

func TestGoogleOAuthBindingProofAndUnbindLockout(t *testing.T) {
	f := setupGoogleOAuthTest(t)
	denied := securityEnrollmentRequest("POST", "/api/oauth/state", `{"provider":"google","intent":"bind"}`, "", f.identity, GenerateOAuthCode)
	assert.NotContains(t, denied.Body.String(), "authorization_url")
	state, code := f.authorization(t, "bind", f.identity, "", googleIdentityClaims("linked"))
	r := f.callback(state, code, f.identity)
	require.Contains(t, r.Body.String(), `"success":true`)
	user, err := model.GetUserByGoogleID("linked")
	require.NoError(t, err)
	assert.Equal(t, f.user.Id, user.Id)
	r = securityEnrollmentRequest("DELETE", "/api/user/bindings/google", "", "", f.identity, UnbindGoogleOAuth)
	assert.Contains(t, r.Body.String(), "SECURITY_PROOF_REQUIRED")
	proof := issueSecurityEnrollmentProof(t, f.identity, service.VerificationOperation{Scope: service.VerificationScopeAccountUnbind, Context: []byte(`{"provider":"google"}`)}, service.VerificationMethodPassword)
	r = securityEnrollmentRequest("DELETE", "/api/user/bindings/google", "", proof, f.identity, UnbindGoogleOAuth)
	require.Contains(t, r.Body.String(), `"success":true`)
	assert.False(t, model.IsGoogleIDAlreadyTaken("linked"))
	var rotation struct {
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(r.Body.Bytes(), &rotation))
	f.identity, err = service.ParseAccessToken(rotation.Data.AccessToken)
	require.NoError(t, err)
	require.NoError(t, model.DB.Transaction(func(tx *gorm.DB) error { return model.UpdateGoogleBindingWithTx(tx, f.user.Id, "linked") }))
	require.NoError(t, model.DB.Model(f.user).Update("password", "").Error)
	proof = issueSecurityEnrollmentProof(t, f.identity, service.VerificationOperation{Scope: service.VerificationScopeAccountUnbind, Context: []byte(`{"provider":"google"}`)}, service.VerificationMethodOAuth)
	r = securityEnrollmentRequest("DELETE", "/api/user/bindings/google", "", proof, f.identity, UnbindGoogleOAuth)
	assert.Contains(t, r.Body.String(), "LAST_LOGIN_METHOD")
	assert.True(t, model.IsGoogleIDAlreadyTaken("linked"))
}

func TestGoogleOAuthRespectsBansAndSecondFactor(t *testing.T) {
	f := setupGoogleOAuthTest(t)
	require.NoError(t, model.DB.Transaction(func(tx *gorm.DB) error { return model.UpdateGoogleBindingWithTx(tx, f.user.Id, "protected") }))
	require.NoError(t, model.DB.Create(&model.TwoFA{UserId: f.user.Id, Secret: "JBSWY3DPEHPK3PXP", IsEnabled: true}).Error)
	state, code := f.authorization(t, "login", service.AuthIdentity{}, "", googleIdentityClaims("protected"))
	r := f.callback(state, code, service.AuthIdentity{})
	assert.Contains(t, r.Body.String(), "require_verification")
	assert.NotContains(t, r.Body.String(), `"access_token"`)
	require.NoError(t, model.DB.Model(f.user).Update("status", common.UserStatusDisabled).Error)
	state, code = f.authorization(t, "login", service.AuthIdentity{}, "", googleIdentityClaims("protected"))
	r = f.callback(state, code, service.AuthIdentity{})
	assert.Contains(t, r.Body.String(), "User has been banned")
	assert.NotContains(t, r.Body.String(), `"access_token"`)
}

func TestGoogleOAuthConfigurationAndSecretRedaction(t *testing.T) {
	f := setupGoogleOAuthTest(t)
	_ = f
	for _, address := range []string{"http://example.invalid", "https://user:pass@example.invalid", "https://example.invalid/path", "https://example.invalid?next=evil", "https://example.invalid#evil", ""} {
		_, err := oauth.GoogleRedirectURI(address)
		assert.Error(t, err, address)
	}
	for _, address := range []string{"https://example.invalid", "http://localhost:3000", "http://127.0.0.1:3000", "http://[::1]:3000"} {
		_, err := oauth.GoogleRedirectURI(address)
		assert.NoError(t, err, address)
	}
	require.NoError(t, model.UpdateOption("google.client_secret", "stored-secret"))
	response := securityEnrollmentRequest("GET", "/api/option/", "", "", f.identity, GetOptions)
	assert.NotContains(t, response.Body.String(), "stored-secret")
	assert.Contains(t, response.Body.String(), `"key":"google.client_secret_configured","value":"true"`)
	response = securityEnrollmentRequest("PUT", "/api/option/", `{"key":"google.client_secret","value":""}`, "", f.identity, UpdateOption)
	assert.Contains(t, response.Body.String(), `"success":true`)
	assert.Equal(t, "stored-secret", system_setting.GetGoogleSettings().ClientSecret)
	common.GoogleOAuthEnabled = false
	response = securityEnrollmentRequest("POST", "/api/oauth/state", `{"provider":"google","intent":"login"}`, "", service.AuthIdentity{}, GenerateOAuthCode)
	assert.Contains(t, response.Body.String(), "GOOGLE_OAUTH_NOT_CONFIGURED")
	assert.Empty(t, response.Result().Cookies())
}

func TestGoogleOAuthSecurityVerificationRequiresRecentGoogleAuthentication(t *testing.T) {
	f := setupGoogleOAuthTest(t)
	require.NoError(t, model.DB.Transaction(func(tx *gorm.DB) error { return model.UpdateGoogleBindingWithTx(tx, f.user.Id, "verify-owner") }))
	require.NoError(t, model.DB.Model(f.user).Update("password", "").Error)
	for _, test := range []struct {
		name     string
		authTime any
		subject  string
		want     string
	}{
		{"missing auth time", nil, "verify-owner", "GOOGLE_REAUTHENTICATION_REQUIRED"},
		{"old authentication", time.Now().Add(-time.Hour).Unix(), "verify-owner", "GOOGLE_REAUTHENTICATION_REQUIRED"},
		{"future authentication", time.Now().Add(time.Hour).Unix(), "verify-owner", "GOOGLE_REAUTHENTICATION_REQUIRED"},
		{"different Google account", time.Now().Unix(), "another-account", "OAUTH_ACCOUNT_MISMATCH"},
		{"fresh authentication", time.Now().Unix(), "verify-owner", `"proof_token"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			claims := googleIdentityClaims(test.subject)
			if test.authTime != nil {
				claims["auth_time"] = test.authTime
			}
			state, code := f.authorization(t, "verify", f.identity, service.VerificationScopePasswordSet, claims)
			r := f.callback(state, code, f.identity)
			assert.Contains(t, r.Body.String(), test.want)
			assert.Equal(t, http.StatusForbidden, f.callback(state, code, f.identity).Code)
		})
	}
}

func TestGoogleOAuthConcurrentCallbackExchangesCodeOnce(t *testing.T) {
	f := setupGoogleOAuthTest(t)
	require.NoError(t, model.DB.Transaction(func(tx *gorm.DB) error { return model.UpdateGoogleBindingWithTx(tx, f.user.Id, "single") }))
	state, code := f.authorization(t, "login", service.AuthIdentity{}, "", googleIdentityClaims("single"))
	start := make(chan struct{})
	results := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		go func() { <-start; results <- f.callback(state, code, service.AuthIdentity{}) }()
	}
	close(start)
	successes := 0
	for range 2 {
		r := <-results
		var body securityEnrollmentResponse
		require.NoError(t, common.Unmarshal(r.Body.Bytes(), &body))
		if body.Success {
			successes++
		} else {
			assert.Equal(t, http.StatusForbidden, r.Code)
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, f.exchanges)
}

func TestGoogleOAuthSecretsNeverReachSQLLogsAndFailedConfigIsNotPublished(t *testing.T) {
	f := setupGoogleOAuthTest(t)
	var output bytes.Buffer
	previousDB, previousDebug := model.DB, common.DebugEnabled
	model.DB = model.DB.Session(&gorm.Session{Logger: logger.New(log.New(&output, "", 0), logger.Config{LogLevel: logger.Info, ParameterizedQueries: false})})
	common.DebugEnabled = true
	t.Cleanup(func() { model.DB = previousDB; common.DebugEnabled = previousDebug })
	require.NoError(t, model.UpdateOption("google.client_secret", "private-google-client-secret"))
	state, _ := f.authorization(t, "login", service.AuthIdentity{}, "", googleIdentityClaims("logging"))
	flow, err := model.GetAuthFlow(state, model.AuthFlowMatch{Purpose: model.AuthFlowPurposeOAuth, Provider: "google"})
	require.NoError(t, err)
	var payload oauthFlowPayload
	require.NoError(t, common.UnmarshalJsonStr(flow.Payload, &payload))
	assert.NotContains(t, output.String(), "private-google-client-secret")
	assert.NotContains(t, output.String(), payload.Google.CodeVerifier)
	assert.NotContains(t, output.String(), payload.Google.Nonce)
	assert.NotContains(t, output.String(), payload.Google.BrowserHash)
	require.NoError(t, model.DB.Callback().Create().Before("gorm:create").Register("google_option_write_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "options" {
			tx.AddError(errors.New("rejected secret: never-publish-secret"))
		}
	}))
	t.Cleanup(func() { _ = model.DB.Callback().Create().Remove("google_option_write_failure") })
	err = model.UpdateOption("google.client_secret", "never-publish-secret")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "never-publish-secret")
	assert.NotContains(t, output.String(), "never-publish-secret")
	assert.Equal(t, "private-google-client-secret", system_setting.GetGoogleSettings().ClientSecret)
	assert.Equal(t, "private-google-client-secret", common.OptionMap["google.client_secret"])
}

func TestGoogleOAuthSecureCookieConfigurationGate(t *testing.T) {
	setupGoogleOAuthTest(t)
	for _, test := range []struct {
		name    string
		secure  bool
		trusted []string
		address string
		ok      bool
	}{
		{"HTTPS missing secure", false, []string{"https://example.com"}, "https://example.com", false},
		{"HTTPS wrong trusted site", true, []string{"https://other.invalid"}, "https://example.com", false},
		{"HTTPS missing trusted site", true, nil, "https://example.com", false},
		{"HTTPS secure trusted", true, []string{"https://example.com"}, "https://example.com", true},
		{"loopback development", false, nil, "http://127.0.0.1:3000", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			common.SessionCookieSecure, common.SessionCookieTrustedURLs = test.secure, test.trusted
			system_setting.ServerAddress = test.address
			err := oauth.GoogleConfigurationError()
			if test.ok {
				assert.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, oauth.ErrGoogleSecureCookiesRequired)
			}
		})
	}
}
