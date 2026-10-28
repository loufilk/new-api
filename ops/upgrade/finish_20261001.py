"""Apply narrowly checked integration fixes after the pinned upstream/media merge.

Never changes a Git ref or a production database. All replacements require the
reviewed preimage; rerunning against a different source fails rather than guesses.
"""
from pathlib import Path
import re

ROOT = Path('.')


def edit(name, old, new):
    path = ROOT / name
    text = path.read_text()
    if text.count(old) != 1:
        raise RuntimeError(f'{name}: expected exactly one reviewed preimage')
    path.write_text(text.replace(old, new, 1))


edit('middleware/access_token_routes.go', '\t// router/api-router.go: /api/user (admin)', '''\t// ChuanAPI routes retain host role and ownership checks in addition to scopes.
\t"GET /api/user/self/email/verification": accessTokenScopeRule("account_security:write"),
\t"GET /api/user/tokens/:id/models": accessTokenScopeRule("api_key:read"),
\t"POST /api/token/models": accessTokenScopeRule("api_key:read"),
\t"POST /api/option/group-rename": accessTokenScopeRule("option:write"),
\t"GET /api/user/wallet": accessTokenScopeRule("wallet:read"),
\t"GET /api/user/wallet/ledger": accessTokenScopeRule("wallet:read"),
\t"GET /api/user/referral/rewards": accessTokenScopeRule("wallet:read"),
\t"GET /api/models/bonus_eligible/audits": accessTokenScopeRule("model:read"),
\t"POST /api/models/bonus_eligible/batch": accessTokenScopeRule("model:write"),
\t// Management PATs must not obtain inference access through the playground.
\t"GET /api/user/playground/bootstrap": accessTokenSessionRule,
\t"POST /api/user/playground/chat": accessTokenSessionRule,
\t// Support has no dedicated PAT scope. Require login sessions rather than
\t// grant ticket access through unrelated profile or user permissions.
\t"GET /api/support/tickets": accessTokenSessionRule,
\t"POST /api/support/tickets": accessTokenSessionRule,
\t"GET /api/support/tickets/:id": accessTokenSessionRule,
\t"POST /api/support/tickets/:id/replies": accessTokenSessionRule,
\t"POST /api/support/tickets/:id/read": accessTokenSessionRule,
\t"GET /api/admin/support/tickets": accessTokenSessionRule,
\t"GET /api/admin/support/tickets/:id": accessTokenSessionRule,
\t"POST /api/admin/support/tickets/:id/replies": accessTokenSessionRule,
\t"POST /api/admin/support/tickets/:id/read": accessTokenSessionRule,
\t"PUT /api/admin/support/tickets/:id/status": accessTokenSessionRule,

\t// router/api-router.go: /api/user (admin)''')

edit('router/access_token_scope_test.go', '"github.com/QuantumNous/new-api/common"', '"github.com/QuantumNous/new-api/common"\n\t"github.com/QuantumNous/new-api/extension"')
edit('router/access_token_scope_test.go', '\t// Anonymous account entry points.', '''\t// Public health/configuration and the refresh-cookie-authenticated probe.
\t"GET /api/healthz",
\t"HEAD /api/healthz",
\t"GET /api/readyz",
\t"HEAD /api/readyz",
\t"GET /api/chuanapi/public-config",
\t"HEAD /api/chuanapi/public-config",
\t"GET /api/user/auth/session",

\t// Anonymous account entry points.''')
edit('router/access_token_scope_test.go', '\t\tSetApiRouter(engine)', '\t\tscopes := SetApiRouter(engine)')
edit('router/access_token_scope_test.go', '\t\tSetTaskRouter(engine)\n', '''\t\tSetTaskRouter(engine)
\t\t// Use production composition, including deferred payment, wallet,
\t\t// support and console routes. Do not remove their permission rules.
\t\treg := newChuanRouteRegistry(t)
\t\trequire.NoError(t, reg.ApplyRoutesWithScopes(scopes, extension.PriorityBeforePlugin))
\t\trequire.NoError(t, reg.ApplyRoutesWithScopes(scopes, extension.PriorityAfterPlugin))
''')

fixture = ROOT / 'router/scoped_token_fixture_test.go'
if fixture.exists():
    raise RuntimeError('unexpected existing scoped token fixture')
fixture.write_text('''package router

import (
 "strings"
 "testing"

 "github.com/QuantumNous/new-api/common"
 "github.com/QuantumNous/new-api/model"
 "github.com/stretchr/testify/require"
 "gorm.io/gorm"
)

// seedRouteScopedToken stores only a fingerprint and exercises real PAT lookup.
func seedRouteScopedToken(t *testing.T, db *gorm.DB, userID int, raw string, scopes ...string) *model.UserAccessToken {
 t.Helper()
 require.True(t, strings.HasPrefix(raw, model.AccessTokenPrefix))
 token := &model.UserAccessToken{
  UserId: userID, Name: "route-contract", TokenHash: model.AccessTokenFingerprint(raw),
  ExpiresAt: common.GetTimestamp()+3600,
 }
 require.NoError(t, token.SetScopes(scopes))
 require.NoError(t, db.Create(token).Error)
 return token
}
''')

name = 'router/group_rename_router_test.go'
edit(name, '\t\t&model.Log{}, &model.AuditLog{},', '\t\t&model.Log{}, &model.AuditLog{}, &model.UserAccessToken{},')
for role in ['common', 'admin', 'root']:
    edit(name, f'"route-{role}-token"', f'model.AccessTokenPrefix + "route-{role}-token"')
    edit(name, f', AccessToken: &{role}Token', '')
edit(name, '\tfor i := range users {\n\t\trequire.NoError(t, db.Create(&users[i]).Error)\n\t}', '''\tfor i := range users {
\t\trequire.NoError(t, db.Create(&users[i]).Error)
\t}
\tseedRouteScopedToken(t, db, users[0].Id, commonToken, "profile:read")
\tseedRouteScopedToken(t, db, users[1].Id, adminToken, "user:read")
\tseedRouteScopedToken(t, db, users[2].Id, rootToken, "option:write")
\trootReadToken := model.AccessTokenPrefix + "route-root-read-token"
\tseedRouteScopedToken(t, db, users[2].Id, rootReadToken, "option:read")''')
edit(name, '\trootRecorder := request(rootToken)', '''\treadOnly := request(rootReadToken)
\trequire.Equal(t, http.StatusForbidden, readOnly.Code)
\tassert.Contains(t, readOnly.Body.String(), "ACCESS_TOKEN_SCOPE_DENIED", "root role must not bypass the token grant")

\trootRecorder := request(rootToken)''')

name = 'router/referral_routes_test.go'
edit(name, '\t\t&model.Log{},', '\t\t&model.Log{},\n\t\t&model.AuditLog{},\n\t\t&model.UserAccessToken{},')
edit(name, '\tif token != "" {\n\t\tuser.AccessToken = &token\n\t}\n\trequire.NoError(t, db.Create(&user).Error)', '''\trequire.NoError(t, db.Create(&user).Error)
\tif token != "" {
\t\tseedRouteScopedToken(t, db, user.Id, token, "wallet:read", "wallet:write")
\t}''')
path = ROOT / name
text, count = re.subn(r'(\w+\s*:= )strings.Repeat\(("[a-g]", 32)\)', r'\1model.AccessTokenPrefix + strings.Repeat(\2)', path.read_text())
if count != 6:
    raise RuntimeError(f'expected 6 referral credential fixtures, got {count}')
path.write_text(text)
edit(name, '// 已撤销凭据：清空 access_token 后原 token 立即失效\n\trequire.NoError(t, db.Model(&model.User{}).Where("id = ?", 701).Update("access_token", nil).Error)', '''// Positive control proves the credential was usable before revocation.
\tresponse = performReferralRouteRequest(engine, http.MethodGet, "/api/user/referral/rewards", "", activeToken)
\trequire.Equal(t, http.StatusOK, response.Code, response.Body.String())
\t// Deleting a scoped PAT immediately revokes the previously valid credential.
\trequire.NoError(t, db.Where("token_hash = ?", model.AccessTokenFingerprint(activeToken)).Delete(&model.UserAccessToken{}).Error)''')

# Preserve the complete upstream precision/translation test, in the correct module.
old = ROOT / 'web/src/features/wallet/__tests__/copy.test.tsx'
new = ROOT / 'web/src/extensions/chuanapi/wallet/__tests__/recharge-copy.test.tsx'
if new.exists():
    raise RuntimeError('unexpected existing recharge copy test')
old.rename(new)
for directory in [old.parent, old.parent.parent / 'components/dialogs', old.parent.parent / 'components', old.parent.parent]:
    if directory.exists():
        directory.rmdir()  # Refuses to delete any nonempty directory.

regression = ROOT / 'router/chuanapi_access_token_test.go'
if regression.exists():
    raise RuntimeError('unexpected existing scoped route regression')
regression.write_text('''package router

import (
 "fmt"
 "net/http"
 "testing"

 "github.com/QuantumNous/new-api/common"
 "github.com/QuantumNous/new-api/model"
 "github.com/stretchr/testify/assert"
 "github.com/stretchr/testify/require"
)

func TestChuanAPIAccessTokenScopeEnforcement(t *testing.T) {
 _, db := setupReferralRouteTest(t)
 engine := newAccessTokenScopeTestEngine(t)
 user := seedReferralRouteUser(t, db, 710, "", 1_000_000, common.UserStatusEnabled)
 reader := model.AccessTokenPrefix + "wallet-reader"
 seedRouteScopedToken(t, db, user.Id, reader, "wallet:read")
 for _, path := range []string{"/api/user/wallet", "/api/user/wallet/ledger", "/api/user/referral/rewards"} {
  response := performReferralRouteRequest(engine, http.MethodGet, path, "", reader)
  require.Equal(t, http.StatusOK, response.Code, response.Body.String())
  assert.Contains(t, response.Body.String(), `"success":true`)
 }
 for index, tc := range []struct {
  method, path, scope, code string
 }{
  {http.MethodGet, "/api/user/wallet", "profile:read", "ACCESS_TOKEN_SCOPE_DENIED"},
  {http.MethodGet, "/api/user/wallet/ledger", "profile:read", "ACCESS_TOKEN_SCOPE_DENIED"},
  {http.MethodGet, "/api/user/referral/rewards", "profile:read", "ACCESS_TOKEN_SCOPE_DENIED"},
  {http.MethodPost, "/api/user/aff_transfer", "wallet:read", "ACCESS_TOKEN_SCOPE_DENIED"},
  {http.MethodPost, "/api/token/models", "wallet:read", "ACCESS_TOKEN_SCOPE_DENIED"},
  {http.MethodGet, "/api/user/tokens/123/models", "wallet:read", "ACCESS_TOKEN_SCOPE_DENIED"},
  {http.MethodGet, "/api/user/self/email/verification", "account_security:read", "ACCESS_TOKEN_SCOPE_DENIED"},
  {http.MethodGet, "/api/user/playground/bootstrap", "api_key:write", "AUTH_SESSION_REQUIRED"},
  {http.MethodPost, "/api/user/playground/chat", "api_key:write", "AUTH_SESSION_REQUIRED"},
  {http.MethodGet, "/api/support/tickets", "profile:read", "AUTH_SESSION_REQUIRED"},
 } {
  t.Run(tc.method+" "+tc.path, func(t *testing.T) {
   raw := model.AccessTokenPrefix + fmt.Sprintf("scoped-route-%d", index)
   seedRouteScopedToken(t, db, user.Id, raw, tc.scope)
   response := performReferralRouteRequest(engine, tc.method, tc.path, `{}`, raw)
   assert.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
   assert.Contains(t, response.Body.String(), tc.code)
  })
 }
 assert.Zero(t, countReferralRouteLedgerRows(t, db, user.Id), "permission denial must not change funds")
 require.NoError(t, db.Model(&model.UserAccessToken{}).Where("token_hash = ?", model.AccessTokenFingerprint(reader)).Update("expires_at", common.GetTimestamp()-1).Error)
 expired := performReferralRouteRequest(engine, http.MethodGet, "/api/user/wallet", "", reader)
 assert.Equal(t, http.StatusUnauthorized, expired.Code)
 assert.Contains(t, expired.Body.String(), "ACCESS_TOKEN_EXPIRED")
}
''')
print('Applied route composition, scoped-token regressions and wallet test ownership fixes.')
