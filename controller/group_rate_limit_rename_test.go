package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGroupRateLimitRenamePreservesCounterAndIsolatesReuseHTTP(t *testing.T) {
	if os.Getenv("TEST_POSTGRES_DSN") == "" {
		t.Skip("requires owned PostgreSQL fixture")
	}
	mode := os.Getenv("GROUP_RATE_RENAME_CHILD")
	if mode == "" {
		for _, mode := range []string{"total-memory", "success-memory", "total-redis", "success-redis"} {
			t.Run(mode, func(t *testing.T) {
				cmd := exec.Command(os.Args[0], "-test.run=^TestGroupRateLimitRenamePreservesCounterAndIsolatesReuseHTTP$", "-test.v", "-test.timeout=2m")
				cmd.Env = append(os.Environ(), "GROUP_RATE_RENAME_CHILD="+mode)
				output, err := cmd.CombinedOutput()
				require.NoError(t, err, string(output))
				t.Log(string(output))
			})
		}
		return
	}
	require.NoError(t, i18n.Init())
	db := setupGroupRenameControllerTest(t)
	limits := "[1,0]"
	pendingSuccess := strings.HasPrefix(mode, "success")
	if pendingSuccess {
		limits = "[0,1]"
	}
	if strings.HasSuffix(mode, "redis") {
		server := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
		require.NoError(t, rdb.Ping(context.Background()).Err())
		common.RedisEnabled, common.RDB = true, rdb
		t.Cleanup(func() { _ = rdb.Close(); common.RedisEnabled = false })
	}
	for key, value := range map[string]string{"GroupRatio": `{"old":1}`, "UserUsableGroups": `{"old":"Old"}`, "ModelRequestRateLimitGroup": fmt.Sprintf(`{"old":%s}`, limits)} {
		require.NoError(t, db.Create(&model.Option{Key: key, Value: value}).Error)
	}
	setting.ModelRequestRateLimitEnabled = true
	setting.ModelRequestRateLimitDurationMinutes = 1
	setting.ModelRequestRateLimitCount, setting.ModelRequestRateLimitSuccessCount = 100, 0
	user := model.User{Username: "rate-rename", AffCode: "rate-rename", Group: "old", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: strings.Repeat("b", 48), Group: "old", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	require.NoError(t, db.Create(&token).Error)
	router := gin.New()
	forwarded := 0
	entered, release := make(chan struct{}), make(chan struct{})
	var first sync.Once
	router.GET("/limited", middleware.TokenAuth(), middleware.ModelRequestRateLimit(), func(c *gin.Context) {
		forwarded++
		if pendingSuccess {
			first.Do(func() { close(entered); <-release })
		}
		c.Status(http.StatusOK)
	})
	perform := func(key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/limited", nil)
		req.Header.Set("Authorization", "Bearer sk-"+key)
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}
	request := func(key string, want int) {
		out := perform(key)
		require.Equal(t, want, out.Code, out.Body.String())
	}
	var firstResponse chan *httptest.ResponseRecorder
	if pendingSuccess {
		firstResponse = make(chan *httptest.ResponseRecorder, 1)
		go func() { firstResponse <- perform(token.Key) }()
		<-entered
	} else {
		request(token.Key, http.StatusOK)
	}
	request(token.Key, http.StatusTooManyRequests)
	_, err := model.RenameGroups([]model.GroupRenameMapping{{From: "old", To: "new"}})
	require.NoError(t, err)
	request(token.Key, http.StatusTooManyRequests)
	if pendingSuccess {
		close(release)
		out := <-firstResponse
		require.Equal(t, http.StatusOK, out.Code, out.Body.String())
		request(token.Key, http.StatusTooManyRequests)
	}
	require.Equal(t, 1, forwarded, "rename must not reset used or in-flight successful quota within the limiter window")
	_, err = model.ApplyGroupRename(model.GroupRenameApplyRequest{Options: map[string]string{"GroupRatio": `{"new":1,"old":1}`, "UserUsableGroups": `{"new":"New","old":"Independent"}`, "ModelRequestRateLimitGroup": fmt.Sprintf(`{"new":%s,"old":%s}`, limits, limits)}})
	require.NoError(t, err)
	reused := model.Token{UserId: user.Id, Key: strings.Repeat("r", 48), Group: "old", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	require.NoError(t, reused.Insert())
	request(reused.Key, http.StatusOK)
	request(reused.Key, http.StatusTooManyRequests)
	request(token.Key, http.StatusTooManyRequests)
	require.Equal(t, 2, forwarded, "separate group budgets must not borrow or reset each other")
}
