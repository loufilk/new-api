package router

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// During a routing-config outage, a browser may recover only its own paid
// receipts; this does not authorize choosing a route or starting a task.
func TestKeyDiagnosticHistoryRecoversWithoutRoutingOrNewAdmission(t *testing.T) {
	setupRelayRouterTestDB(t)
	db := model.DB
	require.NoError(t, db.AutoMigrate(&model.UserSession{}, &model.UserAccessToken{}, &model.AgentInvitation{}, &model.AuditLog{}, &model.ModelTestRun{}, &model.BillingFinalization{}))
	user := func(name string) (int, *service.AuthBundle) {
		u := model.User{Username: name, AffCode: name, Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1, Group: "default"}
		require.NoError(t, db.Create(&u).Error)
		auth, err := service.CreateLoginSession(u.Id, "password", "127.0.0.1", "fixture-browser")
		require.NoError(t, err)
		return u.Id, auth
	}
	owner, session := user("receipt-owner")
	_, other := user("other-reader")
	pat := model.AccessTokenPrefix + "synthetic-diagnostic-test-pat"
	seedRouteScopedToken(t, db, owner, pat, "profile:read")
	require.NoError(t, db.Create(&model.Option{Key: "GroupConfigVersion", Value: "malformed-primary-version"}).Error)
	id := uuid.NewString()
	row := model.ModelTestRun{ID: id, UserID: owner, TokenID: 10, Source: model.KeyDiagnosticSource, Status: "completed", Model: "fixture", BenchmarkID: "candy-logic-v1", RequestID: "fixture-" + id, CreatedAt: time.Now().Unix(), ArtworkHTML: "synthetic-private-answer"}
	require.NoError(t, db.Create(&row).Error)
	engine := gin.New()
	registerPlaygroundRoutes(engine.Group("/api"))
	const prefix = "/api/user/key-diagnostics"
	send := func(method, path, token string) *httptest.ResponseRecorder {
		return performReferralRouteRequest(engine, method, prefix+path, "", token)
	}
	for _, suffix := range []string{"/runs", "/runs/" + id} {
		own := send("GET", suffix, session.AccessToken)
		require.Equal(t, 200, own.Code, own.Body.String())
		assert.Contains(t, own.Body.String(), id)
		if suffix != "/runs" {
			assert.Contains(t, own.Body.String(), "synthetic-private-answer")
		}
		assert.Equal(t, 401, send("GET", suffix, "").Code)
		patReply := send("GET", suffix, pat)
		assert.Equal(t, 403, patReply.Code, patReply.Body.String())
		assert.Contains(t, patReply.Body.String(), "AUTH_SESSION_REQUIRED")
		foreign := send("GET", suffix, other.AccessToken)
		assert.NotContains(t, foreign.Body.String(), id)
		assert.NotContains(t, foreign.Body.String(), "synthetic-private-answer")
		if suffix == "/runs" {
			assert.Equal(t, 200, foreign.Code)
			assert.Contains(t, foreign.Body.String(), `"data":[]`)
		} else {
			assert.Equal(t, 404, foreign.Code)
			assert.Contains(t, foreign.Body.String(), "run_not_found")
		}
	}
	for _, endpoint := range []struct{ method, path string }{{http.MethodPost, "/runs"}, {"GET", "/options"}, {"GET", "/keys/10/models"}} {
		r := send(endpoint.method, endpoint.path, session.AccessToken)
		require.Equal(t, 503, r.Code, r.Body.String())
		assert.Contains(t, r.Body.String(), "GROUP_CONFIG_CHANGED")
	}
	revoked, err := model.RevokeUserSession(owner, session.Session.SID, "revoke-test")
	require.NoError(t, err)
	require.True(t, revoked)
	assert.Equal(t, 401, send("GET", "/runs/"+id, session.AccessToken).Code)
	var rows, charges int64
	require.NoError(t, db.Model(&model.ModelTestRun{}).Count(&rows).Error)
	require.NoError(t, db.Model(&model.BillingFinalization{}).Count(&charges).Error)
	assert.EqualValues(t, 1, rows)
	assert.Zero(t, charges)
}
