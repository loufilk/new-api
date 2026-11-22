package oauth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gin-gonic/gin"
	"golang.org/x/oauth2"
)

const (
	GoogleIssuer                = "https://accounts.google.com"
	googleAuthorizationEndpoint = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenEndpoint         = "https://oauth2.googleapis.com/token"
	googleJWKSURI               = "https://www.googleapis.com/oauth2/v3/certs"
	GoogleOAuthFlowContextKey   = "google_oauth_flow"
)

var (
	ErrGoogleOAuthNotConfigured       = errors.New("Google OAuth is not configured or enabled. Please contact your administrator.")
	ErrGoogleOAuthConflict            = errors.New("The google OAuth provider name is reserved. Rename the conflicting custom provider before enabling Google sign-in.")
	ErrGoogleOAuthFailed              = errors.New("Google authorization failed. Please start Google sign-in again.")
	ErrGoogleReauthenticationRequired = errors.New("Google requires a recent sign-in for this security action. Sign in to Google again, or use a password, two-factor authentication or Passkey.")
	ErrGoogleSecureCookiesRequired    = errors.New("Google sign-in requires secure session cookies and a trusted HTTPS site origin. Ask your administrator to configure the session cookie settings.")
)

// GoogleOAuthFlow is server-owned. Only AuthorizationURL is returned to the client.
// No access/refresh token is stored or used to access Google APIs.
type GoogleOAuthFlow struct {
	CodeVerifier     string `json:"code_verifier"`
	Nonce            string `json:"nonce"`
	ClientID         string `json:"client_id"`
	RedirectURI      string `json:"redirect_uri"`
	BrowserHash      string `json:"browser_hash"`
	RequireFreshAuth bool   `json:"require_fresh_auth,omitempty"`
}

// GoogleRedirectURI pins the callback to the configured site origin, never Host,
// forwarded headers or a caller-provided next/redirect_uri parameter.
func GoogleRedirectURI(address string) (string, error) {
	origin, err := common.NormalizeOrigin(address)
	if err != nil {
		return "", ErrGoogleOAuthNotConfigured
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return "", ErrGoogleOAuthNotConfigured
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && (host == "localhost" || ip != nil && ip.IsLoopback())) {
		return "", ErrGoogleOAuthNotConfigured
	}
	return origin + "/oauth/google", nil
}

func GoogleConfigurationError() error {
	if HasCustomProviderConflict("google") {
		return ErrGoogleOAuthConflict
	}
	config := system_setting.GoogleSettingsSnapshot()
	if !config.Enabled {
		return ErrGoogleOAuthNotConfigured
	}
	return ValidateGoogleConfiguration(config)
}

// ValidateGoogleConfiguration also guards initial enablement, before Enabled is true.
func ValidateGoogleConfiguration(config system_setting.GoogleConfiguration) error {
	if !config.IsConfigured() {
		return ErrGoogleOAuthNotConfigured
	}
	redirect, err := GoogleRedirectURI(config.ServerAddress)
	if err != nil {
		return err
	}
	origin := strings.TrimSuffix(redirect, "/oauth/google")
	if strings.HasPrefix(origin, "https://") && (!common.SessionCookieSecure || !slices.Contains(common.SessionCookieTrustedURLs, origin)) {
		return ErrGoogleSecureCookiesRequired
	}
	return nil
}

func NewGoogleOAuthFlow() (*GoogleOAuthFlow, error) {
	if err := GoogleConfigurationError(); err != nil {
		return nil, err
	}
	config := system_setting.GoogleSettingsSnapshot()
	redirect, err := GoogleRedirectURI(config.ServerAddress)
	if err != nil {
		return nil, err
	}
	return &GoogleOAuthFlow{CodeVerifier: oauth2.GenerateVerifier(), Nonce: oauth2.GenerateVerifier(), ClientID: strings.TrimSpace(config.ClientID), RedirectURI: redirect}, nil
}

func (flow *GoogleOAuthFlow) AuthorizationURL(state string) string {
	values := url.Values{
		"client_id": {flow.ClientID}, "redirect_uri": {flow.RedirectURI},
		"response_type": {"code"}, "scope": {"openid email profile"},
		"state": {state}, "nonce": {flow.Nonce}, "prompt": {"select_account"},
		"code_challenge":        {oauth2.S256ChallengeFromVerifier(flow.CodeVerifier)},
		"code_challenge_method": {"S256"},
	}
	if flow.RequireFreshAuth {
		// Google may omit auth_time unless its session-metadata feature is enabled.
		// The verifier below fails closed; a new token's iat is NOT reauthentication.
		values.Set("claims", `{"id_token":{"auth_time":{"essential":true}}}`)
	}
	return googleAuthorizationEndpoint + "?" + values.Encode()
}

type GoogleProvider struct {
	client *http.Client
	keys   oidc.KeySet
}

func init() { Register("google", NewGoogleProvider(&http.Client{Timeout: 15 * time.Second})) }

// The transport injection supports signed local mock grants in tests. Production
// endpoints and trusted JWKS are constants and cannot be supplied in settings/JWTs.
func NewGoogleProvider(client *http.Client) *GoogleProvider {
	bounded := *client
	bounded.Timeout = 15 * time.Second
	transport := bounded.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	bounded.Transport = googleResponseTransport{transport}
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &GoogleProvider{client: &bounded, keys: oidc.NewRemoteKeySet(oidc.ClientContext(context.Background(), &bounded), googleJWKSURI)}
}

// Bound both token and JWKS bodies. The OIDC library caches trusted keys but
// otherwise reads an entire JWKS body; do not allow that read to grow unbounded.
type googleResponseTransport struct{ base http.RoundTripper }

func (transport googleResponseTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	response.Body = http.MaxBytesReader(nil, response.Body, 1<<20)
	return response, nil
}

func (p *GoogleProvider) GetName() string { return "Google" }
func (p *GoogleProvider) IsEnabled() bool { return GoogleConfigurationError() == nil }

func (p *GoogleProvider) ExchangeToken(ctx context.Context, code string, c *gin.Context) (*OAuthToken, error) {
	if err := GoogleConfigurationError(); err != nil {
		return nil, err
	}
	value, _ := c.Get(GoogleOAuthFlowContextKey)
	flow, ok := value.(*GoogleOAuthFlow)
	config := system_setting.GoogleSettingsSnapshot()
	redirect, err := GoogleRedirectURI(config.ServerAddress)
	if !ok || flow == nil || flow.CodeVerifier == "" || flow.Nonce == "" || code == "" || len(code) > 4096 || err != nil ||
		flow.ClientID != strings.TrimSpace(config.ClientID) || flow.RedirectURI != redirect {
		return nil, ErrGoogleOAuthFailed
	}
	values := url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "client_id": {flow.ClientID},
		"client_secret": {strings.TrimSpace(config.ClientSecret)}, "redirect_uri": {flow.RedirectURI}, "code_verifier": {flow.CodeVerifier},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, googleTokenEndpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, ErrGoogleOAuthFailed
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := p.client.Do(request)
	if err != nil {
		return nil, ErrGoogleOAuthFailed
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, ErrGoogleOAuthFailed
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return nil, ErrGoogleOAuthFailed
	}
	var token OAuthToken
	if err := common.Unmarshal(raw, &token); err != nil || token.IDToken == "" || len(token.IDToken) > 65536 {
		return nil, ErrGoogleOAuthFailed
	}
	token.ClientID, token.Nonce = flow.ClientID, flow.Nonce
	token.RequireFreshAuth = flow.RequireFreshAuth
	// Authentication only: discard all Google API credentials immediately.
	token.AccessToken, token.RefreshToken = "", ""
	return &token, nil
}

func (p *GoogleProvider) GetUserInfo(ctx context.Context, token *OAuthToken) (*OAuthUser, error) {
	if err := GoogleConfigurationError(); err != nil {
		return nil, err
	}
	config := system_setting.GoogleSettingsSnapshot()
	if token == nil || token.ClientID != strings.TrimSpace(config.ClientID) || token.Nonce == "" {
		return nil, ErrGoogleOAuthFailed
	}
	verifier := oidc.NewVerifier(GoogleIssuer, p.keys, &oidc.Config{ClientID: token.ClientID, SupportedSigningAlgs: []string{oidc.RS256}})
	verified, err := verifier.Verify(ctx, token.IDToken)
	if err != nil {
		return nil, ErrGoogleOAuthFailed
	}
	if verified.Issuer != GoogleIssuer && verified.Issuer != "accounts.google.com" {
		return nil, ErrGoogleOAuthFailed
	}
	var raw json.RawMessage
	if err := verified.Claims(&raw); err != nil {
		return nil, ErrGoogleOAuthFailed
	}
	var claims struct {
		Email           string `json:"email"`
		EmailVerified   bool   `json:"email_verified"`
		HostedDomain    string `json:"hd"`
		Name            string `json:"name"`
		AuthorizedParty string `json:"azp"`
		AuthTime        int64  `json:"auth_time"`
	}
	if err := common.Unmarshal(raw, &claims); err != nil {
		return nil, ErrGoogleOAuthFailed
	}
	if verified.Subject == "" || len(verified.Subject) > 255 || strings.ContainsFunc(verified.Subject, func(r rune) bool { return r < 33 || r > 126 }) ||
		subtle.ConstantTimeCompare([]byte(verified.Nonce), []byte(token.Nonce)) != 1 ||
		verified.IssuedAt.IsZero() || verified.IssuedAt.After(time.Now().Add(time.Minute)) ||
		claims.AuthorizedParty != "" && claims.AuthorizedParty != token.ClientID || len(verified.Audience) > 1 && claims.AuthorizedParty != token.ClientID || !claims.EmailVerified {
		return nil, ErrGoogleOAuthFailed
	}
	if token.RequireFreshAuth && (claims.AuthTime <= 0 || claims.AuthTime < time.Now().Add(-5*time.Minute).Unix() || claims.AuthTime > time.Now().Add(time.Minute).Unix()) {
		return nil, ErrGoogleReauthenticationRequired
	}
	email, err := mail.ParseAddress(claims.Email)
	if err != nil || email.Address != claims.Email || len(claims.Email) > 254 {
		return nil, ErrGoogleOAuthFailed
	}
	name := []rune(claims.Name)
	if len(name) > 20 {
		name = name[:20]
	}
	// Google may have verified a third-party mailbox only when its account was
	// created; that mailbox can since have changed owners. Never turn that stale
	// claim into a local password-recovery credential. Such users still sign in
	// by subject and can add an email through the existing local verification.
	recoveryEmail := ""
	if strings.HasSuffix(strings.ToLower(email.Address), "@gmail.com") || strings.TrimSpace(claims.HostedDomain) != "" {
		recoveryEmail = email.Address
	}
	// No email matching, role/group mapping, hosted-domain privilege or userinfo call.
	return &OAuthUser{ProviderUserID: verified.Subject, Username: "google_" + common.GetRandomString(12), DisplayName: string(name), Email: recoveryEmail}, nil
}

func (p *GoogleProvider) IsUserIDTaken(id string) bool { return model.IsGoogleIDAlreadyTaken(id) }
func (p *GoogleProvider) FillUserByProviderID(user *model.User, id string) error {
	stored, err := model.GetUserByGoogleID(id)
	if err != nil {
		return err
	}
	*user = *stored
	return nil
}
func (p *GoogleProvider) SetProviderUserID(user *model.User, id string) { user.GoogleId = id }
func (p *GoogleProvider) GetProviderPrefix() string                     { return "google_" }
func (p *GoogleProvider) ProviderUserIDColumn() string                  { return "google_id" }
