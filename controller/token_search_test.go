package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearchTokensPostPreservesOwnershipAndCredentialPrivacy(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, i18n.Init())
	require.NoError(t, db.AutoMigrate(&model.UserSession{}, &model.UserAccessToken{}, &model.AuditLog{}))
	previousSecret, previousSearchLimit := common.SessionSecret, common.SearchRateLimitEnable
	common.SessionSecret, common.SearchRateLimitEnable = "synthetic-token-search-session-secret", false
	t.Cleanup(func() {
		common.SessionSecret, common.SearchRateLimitEnable = previousSecret, previousSearchLimit
	})
	user := model.User{Username: "search-owner", Group: "default", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1, AffCode: "search-owner"}
	require.NoError(t, db.Create(&user).Error)
	other := model.User{Username: "search-other", Group: "default", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1, AffCode: "search-other"}
	require.NoError(t, db.Create(&other).Error)
	owned := seedToken(t, db, user.Id, "search-one", "synthetic-owned-search-secret")
	second := seedToken(t, db, user.Id, "search-two", "synthetic-second-search-secret")
	foreign := seedToken(t, db, other.Id, "search-one", "synthetic-foreign-search-secret")
	session := &model.UserSession{
		SID: "synthetic-token-search-session", UserID: user.Id, Version: 1, UserAuthVersion: 1,
		Status: model.UserSessionStatusActive, RefreshHash: "unused", LoginMethod: "password",
		LastActiveAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}
	require.NoError(t, model.CreateUserSession(session))
	jwt, _, err := service.IssueAccessToken(service.AuthIdentity{UserID: user.Id, SessionID: session.SID, UserAuthVersion: 1, SessionVersion: 1})
	require.NoError(t, err)
	readPAT := model.AccessTokenPrefix + "synthetic-key-search-read"
	profilePAT := model.AccessTokenPrefix + "synthetic-key-search-profile"
	for raw, scope := range map[string]string{readPAT: "api_key:read", profilePAT: "profile:read"} {
		pat := model.UserAccessToken{UserId: user.Id, Name: "search-fixture", TokenHash: model.AccessTokenFingerprint(raw), ExpiresAt: time.Now().Add(time.Hour).Unix()}
		require.NoError(t, pat.SetScopes([]string{scope}))
		require.NoError(t, db.Create(&pat).Error)
	}
	router := gin.New()
	routes := router.Group("/api/token", middleware.UserAuth(), middleware.TokenOperationAudit())
	routes.GET("/search", middleware.SearchRateLimit(), SearchTokens)
	routes.POST("/search", middleware.SearchRateLimit(), middleware.DisableCache(), SearchTokens)
	for _, tc := range []struct {
		name, method, path, credential string
		body                           map[string]any
		malformed                      string
		status, total                  int
		ids                            []int
		success                        bool
	}{
		{name: "body name is owner scoped", method: http.MethodPost, path: "/api/token/search?p=1&size=10", credential: jwt, body: map[string]any{"keyword": "search-one"}, status: http.StatusOK, success: true, total: 1, ids: []int{owned.Id}},
		{name: "body secret exact match", method: http.MethodPost, path: "/api/token/search?p=1&size=10", credential: jwt, body: map[string]any{"token": "sk-" + owned.Key}, status: http.StatusOK, success: true, total: 1, ids: []int{owned.Id}},
		{name: "foreign secret does not match", method: http.MethodPost, path: "/api/token/search?p=1&size=10", credential: jwt, body: map[string]any{"token": foreign.Key}, status: http.StatusOK, success: true},
		{name: "body takes precedence over query filters", method: http.MethodPost, path: "/api/token/search?p=1&size=10&keyword=ignored&token=ignored", credential: jwt, body: map[string]any{"token": owned.Key}, status: http.StatusOK, success: true, total: 1, ids: []int{owned.Id}},
		{name: "pagination stays in query", method: http.MethodPost, path: "/api/token/search?p=2&size=1", credential: jwt, body: map[string]any{"keyword": "search-%"}, status: http.StatusOK, success: true, total: 2, ids: []int{owned.Id}},
		{name: "both body filters apply", method: http.MethodPost, path: "/api/token/search?p=1&size=10", credential: jwt, body: map[string]any{"keyword": second.Name, "token": owned.Key}, status: http.StatusOK, success: true},
		{name: "read PAT has the same permission", method: http.MethodPost, path: "/api/token/search?p=1&size=10", credential: readPAT, body: map[string]any{"token": owned.Key}, status: http.StatusOK, success: true, total: 1, ids: []int{owned.Id}},
		{name: "unrelated PAT scope denied", method: http.MethodPost, path: "/api/token/search?p=1&size=10", credential: profilePAT, body: map[string]any{"token": owned.Key}, status: http.StatusForbidden},
		{name: "authentication required", method: http.MethodPost, path: "/api/token/search?p=1&size=10", body: map[string]any{"token": owned.Key}, status: http.StatusUnauthorized},
		{name: "malformed JSON is not echoed", method: http.MethodPost, path: "/api/token/search?p=1&size=10", credential: jwt, malformed: `{"token":"synthetic-malformed-secret",`, status: http.StatusOK},
		{name: "invalid field type is rejected", method: http.MethodPost, path: "/api/token/search?p=1&size=10", credential: jwt, body: map[string]any{"token": []string{"synthetic-malformed-secret"}}, status: http.StatusOK},
		{name: "legacy GET name", method: http.MethodGet, path: "/api/token/search?keyword=search-one&p=1&size=10", credential: jwt, status: http.StatusOK, success: true, total: 1, ids: []int{owned.Id}},
		{name: "legacy GET secret", method: http.MethodGet, path: "/api/token/search?token=" + url.QueryEscape("sk-"+owned.Key) + "&p=1&size=10", credential: jwt, status: http.StatusOK, success: true, total: 1, ids: []int{owned.Id}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body []byte
			if tc.body != nil {
				body, err = common.Marshal(tc.body)
				require.NoError(t, err)
			} else if tc.malformed != "" {
				body = []byte(tc.malformed)
			}
			request := httptest.NewRequest(tc.method, tc.path, bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			if tc.credential != "" {
				request.Header.Set("Authorization", "Bearer "+tc.credential)
			}
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			require.Equal(t, tc.status, recorder.Code, recorder.Body.String())
			response := decodeAPIResponse(t, recorder)
			assert.Equal(t, tc.success, response.Success, response.Message)
			for _, secret := range []string{owned.Key, second.Key, foreign.Key, "synthetic-malformed-secret"} {
				assert.NotContains(t, recorder.Body.String(), secret)
			}
			if tc.method == http.MethodPost && recorder.Code == http.StatusOK {
				assert.Contains(t, recorder.Header().Get("Cache-Control"), "no-store")
				assert.NotContains(t, request.URL.String(), owned.Key)
				assert.NotContains(t, request.URL.String(), foreign.Key)
			}
			if !tc.success {
				return
			}
			var page struct {
				Total int                 `json:"total"`
				Items []tokenResponseItem `json:"items"`
			}
			require.NoError(t, common.Unmarshal(response.Data, &page))
			assert.Equal(t, tc.total, page.Total)
			ids := make([]int, 0, len(page.Items))
			for _, item := range page.Items {
				ids = append(ids, item.ID)
				assert.Equal(t, owned.GetMaskedKey(), item.Key)
			}
			assert.ElementsMatch(t, tc.ids, ids)
		})
	}
	var audit []model.AuditLog
	require.NoError(t, db.Find(&audit).Error)
	auditJSON, err := common.Marshal(audit)
	require.NoError(t, err)
	for _, secret := range []string{owned.Key, second.Key, foreign.Key, readPAT, profilePAT, "synthetic-malformed-secret"} {
		assert.NotContains(t, string(auditJSON), secret, "search bodies and credentials must not enter the audit trail")
	}
}

func TestTokenStatusFilterAppliesBeforePaginationAndSearch(t *testing.T) {
	db, user, original := setupTokenWriteRegression(t)
	first := seedToken(t, db, user.Id, "match first", "synthetic-first-filter-key")
	enabled := seedToken(t, db, user.Id, "match enabled", "synthetic-enabled-filter-key")
	expired := seedToken(t, db, user.Id, "match expired", "synthetic-expired-filter-key")
	last := seedToken(t, db, user.Id, "match last", "synthetic-last-filter-key")
	foreign := seedToken(t, db, user.Id+1, "match foreign", "synthetic-foreign-filter-key")
	deleted := seedToken(t, db, user.Id, "match deleted", "synthetic-deleted-filter-key")
	require.NoError(t, db.Model(&model.Token{}).Where("id IN ?", []int{first.Id, last.Id, foreign.Id, deleted.Id}).Update("status", common.TokenStatusDisabled).Error)
	require.NoError(t, db.Model(expired).Update("status", common.TokenStatusExpired).Error)
	require.NoError(t, db.Delete(deleted).Error)
	for _, tc := range []struct {
		name, method, path string
		body               any
		total              int
		ids                []int
		invalid            bool
	}{
		{name: "unfiltered", method: http.MethodGet, path: "/api/token/?p=1&size=10", total: 5, ids: []int{last.Id, expired.Id, enabled.Id, first.Id, original.Id}},
		{name: "filtered first page", method: http.MethodGet, path: "/api/token/?status=2&p=1&size=1", total: 2, ids: []int{last.Id}},
		{name: "filtered second page", method: http.MethodGet, path: "/api/token/?status=2&p=2&size=1", total: 2, ids: []int{first.Id}},
		{name: "multiple states", method: http.MethodGet, path: "/api/token/?status=2,3&p=1&size=10", total: 3, ids: []int{last.Id, expired.Id, first.Id}},
		{name: "GET search", method: http.MethodGet, path: "/api/token/search?status=2&keyword=match%25&p=2&size=1", total: 2, ids: []int{first.Id}},
		{name: "POST search", method: http.MethodPost, path: "/api/token/search?status=2&p=2&size=1", body: map[string]any{"keyword": "match%"}, total: 2, ids: []int{first.Id}},
		{name: "POST credential match", method: http.MethodPost, path: "/api/token/search?status=2&p=1&size=10", body: map[string]any{"token": first.Key}, total: 1, ids: []int{first.Id}},
		{name: "POST credential filtered out", method: http.MethodPost, path: "/api/token/search?status=1&p=1&size=10", body: map[string]any{"token": first.Key}, ids: []int{}},
		{name: "invalid list status", method: http.MethodGet, path: "/api/token/?status=99", invalid: true},
		{name: "invalid search status", method: http.MethodPost, path: "/api/token/search?status=1,bad", body: map[string]any{"keyword": "match%"}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, recorder := newAuthenticatedContext(t, tc.method, tc.path, tc.body, user.Id)
			if ctx.Request.URL.Path == "/api/token/search" {
				SearchTokens(ctx)
			} else {
				GetAllTokens(ctx)
			}
			response := decodeAPIResponse(t, recorder)
			if tc.invalid {
				assert.False(t, response.Success)
				assert.Equal(t, "Invalid token status", response.Message)
				return
			}
			require.True(t, response.Success, response.Message)
			var page struct {
				Total int                 `json:"total"`
				Items []tokenResponseItem `json:"items"`
			}
			require.NoError(t, common.Unmarshal(response.Data, &page))
			ids := make([]int, 0, len(page.Items))
			for _, token := range page.Items {
				ids = append(ids, token.ID)
			}
			assert.Equal(t, tc.total, page.Total)
			assert.Equal(t, tc.ids, ids)
			assert.NotContains(t, recorder.Body.String(), first.Key)
			assert.NotContains(t, ctx.Request.URL.String(), first.Key)
		})
	}
}
