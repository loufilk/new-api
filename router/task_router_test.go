/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
package router

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Gin panics at registration time when routes sharing a path position use
// different wildcard names, which unit tests that build their own routers
// never catch. Registering against a real engine is the only guard.
func TestSetTaskRouterRegistersWithoutConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	require.NotPanics(t, func() { SetTaskRouter(engine) })

	routes := engine.Routes()
	require.Len(t, routes, 5)
	actual := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		actual[route.Method+" "+route.Path] = struct{}{}
	}
	assert.Contains(t, actual, http.MethodPost+" /v1/tasks/:key")
	assert.Contains(t, actual, http.MethodGet+" /v1/tasks/:key")
	assert.Contains(t, actual, http.MethodGet+" /v1/tasks/:key/artifacts")
	assert.Contains(t, actual, http.MethodGet+" /v1/tasks/:key/artifacts/:artifact_key/content")
	assert.Contains(t, actual, http.MethodHead+" /v1/tasks/:key/artifacts/:artifact_key/content")
	for route := range actual {
		assert.NotContains(t, route, "/native/")
	}
}

// Run in a child because model.InitDB initializes process-wide quoted-column
// caches. The fixture only owns fresh tables on the explicitly supplied DB.
func TestTaskSubmissionRateLimitDoesNotBlockTaskReads(t *testing.T) {
	if os.Getenv("TEST_POSTGRES_DSN") == "" || os.Getenv("TEST_REDIS_DSN") == "" {
		t.Skip("isolated PostgreSQL and Redis required")
	}
	if os.Getenv("TASK_ROUTE_LIMIT_CHILD") != "1" {
		binary, err := os.Executable()
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "-test.run=^"+regexp.QuoteMeta(t.Name())+"$", "-test.v", "-test.count=1", "-test.timeout=45s")
		cmd.Env = append(os.Environ(), "TASK_ROUTE_LIMIT_CHILD=1")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", output)
		require.Contains(t, string(output), "--- PASS: "+t.Name())
		t.Logf("%s", output)
		return
	}
	t.Setenv("SQL_DSN", os.Getenv("TEST_POSTGRES_DSN"))
	t.Setenv("LOG_SQL_DSN", "")
	common.IsMasterNode = false
	require.NoError(t, model.InitDB())
	db := model.DB
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	fixtures := []any{&model.User{}, &model.Token{}, &model.Task{}, &model.Option{}}
	for _, fixture := range fixtures {
		require.False(t, db.Migrator().HasTable(fixture), "fixture refuses existing tables")
	}
	require.NoError(t, db.AutoMigrate(fixtures...))
	t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(fixtures...)) })
	var version string
	require.NoError(t, db.Raw("SELECT version()").Scan(&version).Error)
	t.Log(version)
	options, err := redis.ParseURL(os.Getenv("TEST_REDIS_DSN"))
	require.NoError(t, err)
	common.RDB = redis.NewClient(options)
	common.RedisEnabled = true
	t.Cleanup(func() { require.NoError(t, common.RDB.Close()) })
	require.NoError(t, common.RDB.Ping(t.Context()).Err())
	info, err := common.RDB.Info(t.Context(), "server").Result()
	require.NoError(t, err)
	t.Log(info)
	setting.ModelRequestRateLimitEnabled = true
	setting.ModelRequestRateLimitCount = 1
	setting.ModelRequestRateLimitSuccessCount = 0
	setting.ModelRequestRateLimitDurationMinutes = 1
	user := &model.User{Id: 7300248, Username: "task-route-boundary", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "task-route-boundary"}
	require.NoError(t, db.Create(user).Error)
	token := &model.Token{UserId: user.Id, Key: "synthetictaskroutertoken", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	require.NoError(t, db.Create(token).Error)
	task := &model.Task{TaskID: "task_rate_limit", UserId: user.Id, Platform: "missing-plugin", Status: model.TaskStatusFailure, FailReason: "private vendor key sk-secret https://private.invalid"}
	require.NoError(t, db.Create(task).Error)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetTaskRouter(engine)
	first := performReferralRouteRequest(engine, http.MethodPost, "/v1/tasks/missing-plugin", `{}`, token.Key)
	require.Equal(t, http.StatusBadRequest, first.Code, first.Body.String())
	second := performReferralRouteRequest(engine, http.MethodPost, "/v1/tasks/missing-plugin", `{}`, token.Key)
	require.Equal(t, http.StatusTooManyRequests, second.Code, second.Body.String())
	read := performReferralRouteRequest(engine, http.MethodGet, "/v1/tasks/"+task.TaskID, "", token.Key)
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	assert.Contains(t, read.Body.String(), `"fail_reason":"upstream request failed"`)
	assert.NotContains(t, read.Body.String(), "sk-secret")
	assert.NotContains(t, read.Body.String(), "private.invalid")
	for _, request := range []struct{ method, path string }{
		{http.MethodGet, "/v1/tasks/" + task.TaskID + "/artifacts"},
		{http.MethodGet, "/v1/tasks/" + task.TaskID + "/artifacts/unknown/content"},
		{http.MethodHead, "/v1/tasks/" + task.TaskID + "/artifacts/unknown/content"},
	} {
		response := performReferralRouteRequest(engine, request.method, request.path, "", token.Key)
		assert.NotEqual(t, http.StatusTooManyRequests, response.Code, response.Body.String())
		assert.Contains(t, []int{http.StatusOK, http.StatusNotFound, http.StatusConflict}, response.Code, response.Body.String())
	}
	var stored model.Task
	require.NoError(t, db.First(&stored, task.ID).Error)
	assert.Equal(t, task.FailReason, stored.FailReason)
}
