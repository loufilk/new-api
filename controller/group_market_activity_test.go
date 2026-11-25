package controller

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGroupMarketActivityRootReadEstablishesFence(t *testing.T) {
	if os.Getenv("TEST_TASK_DB_DIALECT") != "postgres" || os.Getenv("TEST_POSTGRES_DSN") == "" {
		t.Skip("requires isolated PostgreSQL 18.6")
	}
	if groupRenameControllerIsolatedProcess(t) {
		return
	}
	db := setupGroupRenameControllerTest(t)
	require.NoError(t, i18n.Init())
	require.NoError(t, db.Create(&model.Option{Key: "GroupRatio", Value: `{"observed":1}`}).Error)
	require.NoError(t, model.UpdateOption("GroupRatio", `{"observed":2}`))
	ordinary := &model.User{Username: "market-reader", Group: "default", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1, AffCode: "market-reader"}
	require.NoError(t, db.Create(ordinary).Error)
	_, ordinaryJWT := createAccessTokenTestSession(t, ordinary.Id, "market-ordinary")
	_, rootJWT := createAccessTokenTestSession(t, 990001, "market-root")
	const path = "/api/option/group-catalog/activity"
	engine := gin.New()
	engine.GET(path, middleware.RootAuth(), GetGroupMarketActivity)
	request := func(bearer string) *httptest.ResponseRecorder {
		out := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		engine.ServeHTTP(out, req)
		return out
	}
	require.Equal(t, http.StatusUnauthorized, request("").Code)
	require.Equal(t, http.StatusForbidden, request(ordinaryJWT).Code)
	out := request(rootJWT)
	require.Equal(t, http.StatusOK, out.Code, out.Body.String())
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Revision int64                                `json:"revision"`
			Groups   map[string]model.GroupMarketActivity `json:"groups"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(out.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Positive(t, response.Data.Revision)
	require.Len(t, response.Data.Groups["observed"].RateChanges, 1)
	require.Equal(t, 2.0, *response.Data.Groups["observed"].RateChanges[0].NewRatio)
}
