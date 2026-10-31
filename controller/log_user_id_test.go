package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func usageLogRequest(handler gin.HandlerFunc, userID int, username string, query string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/log?"+query, nil)
	c.Set("id", userID)
	c.Set("username", username)
	c.Set("role", common.RoleAdminUser)
	handler(c)
	return response
}

// Uses only explicitly configured disposable PostgreSQL, including a physically
// separate log database so usage queries cannot accidentally use the main DB.
func TestUsageLogUserIDPostgres(t *testing.T) {
	dsn := os.Getenv("AUDIT_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires disposable loopback PostgreSQL AUDIT_POSTGRES_DSN")
	}
	gin.SetMode(gin.TestMode)
	for _, separateLogDB := range []bool{false, true} {
		t.Run(fmt.Sprintf("separate_log_db=%t", separateLogDB), func(t *testing.T) {
			db, logDSN := newAuditTestDatabase(t, "postgres", dsn)
			logDB := db
			if separateLogDB {
				logDB, logDSN = newAuditTestDatabase(t, "postgres", dsn)
			}
			useLogExportDatabases(t, db, logDB, common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
			require.NoError(t, db.AutoMigrate(&model.Channel{}))
			require.NoError(t, logDB.AutoMigrate(&model.Log{}))
			// Initialize the log dialect's quoted column names through the real
			// startup path, without unrelated migrations on this focused fixture.
			previousMaster := common.IsMasterNode
			common.IsMasterNode = false
			t.Cleanup(func() { common.IsMasterNode = previousMaster })
			t.Setenv("LOG_SQL_DSN", logDSN)
			require.NoError(t, model.InitLogDB())
			pool, err := model.LOG_DB.DB()
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, pool.Close()) })
			var version string
			require.NoError(t, logDB.Raw("SELECT version()").Scan(&version).Error)
			t.Logf("PostgreSQL usage-log evidence: %s", version)
			settings := operation_setting.GetLogSetting()
			previousSettings := *settings
			settings.UserExportEnabled = true
			t.Cleanup(func() { *settings = previousSettings })
			require.NoError(t, db.Create(&model.Channel{Id: 9, Name: "log-test-channel"}).Error)
			now := time.Now().Unix()
			logs := []model.Log{
				{UserId: 42, Username: "owner-before", CreatedAt: now - 120, Type: model.LogTypeConsume, Quota: 10, RequestId: "owner-old"},
				{UserId: 42, Username: "owner-after", CreatedAt: now - 10, Type: model.LogTypeConsume, Quota: 20, PromptTokens: 2, CompletionTokens: 1, RequestId: "owner-new"},
				{UserId: 42, Username: "owner-after", CreatedAt: now - 5, Type: model.LogTypeError, Quota: 999, RequestId: "owner-error"},
				{UserId: 84, Username: "42", CreatedAt: now - 8, Type: model.LogTypeConsume, Quota: 70, PromptTokens: 30, RequestId: "numeric-username"},
				{UserId: 7, Username: "owner-extra", CreatedAt: now - 9, Type: model.LogTypeConsume, Quota: 90, PromptTokens: 40, RequestId: "other-owner"},
				{UserId: 91, Username: "own_er%", CreatedAt: now - 7, Type: model.LogTypeConsume, Quota: 80, PromptTokens: 50, RequestId: "literal-wildcards"},
				{UserId: 0, Username: "orphan", CreatedAt: now - 6, Type: model.LogTypeConsume, Quota: 1000, RequestId: "orphan"},
				{UserId: 9007199254740993, Username: "large-id", CreatedAt: now - 4, Type: model.LogTypeConsume, Quota: 1, RequestId: "large-id"},
			}
			for i := range logs {
				logs[i].ChannelId = 9
				logs[i].Group = "log-test-group"
				logs[i].TokenName = "log-test-token"
				logs[i].ModelName = "log-test-model"
				logs[i].UpstreamRequestId = "upstream-" + logs[i].RequestId
			}
			require.NoError(t, logDB.Create(&logs).Error)
			recent := fmt.Sprintf("start_timestamp=%d&end_timestamp=%d", now-3600, now+3600)

			t.Run("admin exact ID and numeric username remain distinct", func(t *testing.T) {
				for _, tc := range []struct {
					query string
					ids   []any
					quota int
				}{
					{"user_id=42", []any{"owner-error", "owner-new", "owner-old"}, 30},
					{"user_id=00042", []any{"owner-error", "owner-new", "owner-old"}, 30},
					{"username=42", []any{"numeric-username"}, 70},
					{"user_id=42&username=42", []any{}, 0},
					{"user_id=42&username=owner-after", []any{"owner-error", "owner-new"}, 20},
					{"user_id=42&username=owner-%25", []any{"owner-error", "owner-new", "owner-old"}, 30},
					{"user_id=9999", []any{}, 0},
					{"user_id=9223372036854775807", []any{}, 0},
					{"user_id=9007199254740993", []any{"large-id"}, 1},
				} {
					t.Run(tc.query, func(t *testing.T) {
						query := recent + "&" + tc.query
						listed := callLogHandler(t, GetAllLogs, 100, common.RoleAdminUser, query)[0]
						require.Equal(t, true, listed["success"])
						assert.EqualValues(t, len(tc.ids), listed["data"].(map[string]any)["total"])
						exported := callLogHandler(t, ExportAllLogs, 100, common.RoleAdminUser, query)
						require.Equal(t, true, exported[len(exported)-1]["success"])
						assert.Equal(t, tc.ids, exportedRequestIds(exported))
						stats := callLogHandler(t, GetLogsStat, 100, common.RoleAdminUser, query)[0]
						require.Equal(t, true, stats["success"])
						assert.EqualValues(t, tc.quota, stats["data"].(map[string]any)["quota"])
					})
				}
			})

			t.Run("filters pagination and statistics keep the selected ID", func(t *testing.T) {
				query := recent + "&user_id=42&token_name=log-test-token&model_name=log-test-model&group=log-test-group&channel=9"
				for page, id := range []string{"owner-error", "owner-new", "owner-old"} {
					listed := callLogHandler(t, GetAllLogs, 100, common.RoleAdminUser, query+fmt.Sprintf("&p=%d&page_size=1", page+1))[0]
					require.Equal(t, true, listed["success"])
					data := listed["data"].(map[string]any)
					assert.EqualValues(t, 3, data["total"])
					items := data["items"].([]any)
					require.Len(t, items, 1)
					assert.Equal(t, id, items[0].(map[string]any)["request_id"])
				}
				stats := callLogHandler(t, GetLogsStat, 100, common.RoleAdminUser, query)[0]
				assert.Equal(t, map[string]any{"quota": float64(30), "rpm": float64(1), "tpm": float64(3)}, stats["data"])
				for _, filter := range []string{"request_id=owner-new", "upstream_request_id=upstream-owner-new"} {
					filtered := query + "&" + filter
					stats := callLogHandler(t, GetLogsStat, 100, common.RoleAdminUser, filtered)[0]
					assert.Equal(t, map[string]any{"quota": float64(20), "rpm": float64(1), "tpm": float64(3)}, stats["data"])
					assert.Equal(t, []any{"owner-new"}, exportedRequestIds(callLogHandler(t, ExportAllLogs, 100, common.RoleAdminUser, filtered)))
				}
				// Usage remains consume-only; rates remain rolling, not historical.
				historical := fmt.Sprintf("user_id=42&type=%d&start_timestamp=%d&end_timestamp=%d", model.LogTypeError, now-3600, now-100)
				stats = callLogHandler(t, GetLogsStat, 100, common.RoleAdminUser, historical)[0]
				assert.Equal(t, map[string]any{"quota": float64(10), "rpm": float64(1), "tpm": float64(3)}, stats["data"])
			})

			t.Run("invalid ID never broadens list export or statistics", func(t *testing.T) {
				for _, value := range []string{"0", "-1", "+1", "1.0", "1e2", " 42", "42 ", "４２", "abc", "42 OR 1=1", "9223372036854775808", "18446744073709551615"} {
					for name, handler := range map[string]gin.HandlerFunc{"list": GetAllLogs, "export": ExportAllLogs, "stat": GetLogsStat} {
						t.Run(name+"/"+value, func(t *testing.T) {
							response := usageLogRequest(handler, 100, "admin", recent+"&user_id="+url.QueryEscape(value))
							assert.Equal(t, http.StatusBadRequest, response.Code)
							assert.Contains(t, response.Body.String(), `"success":false`)
							assert.NotContains(t, response.Body.String(), `"data"`)
						})
					}
				}
				for _, query := range []string{"user_id=42&user_id=84", "user_id=&user_id=42", "user_id=%zz", "user_id=42;84", "user_id=42&user_id=%zz"} {
					for _, handler := range []gin.HandlerFunc{GetAllLogs, ExportAllLogs, GetLogsStat} {
						assert.Equal(t, http.StatusBadRequest, usageLogRequest(handler, 100, "admin", query).Code)
					}
				}
				for _, query := range []string{"", "user_id="} {
					listed := callLogHandler(t, GetAllLogs, 100, common.RoleAdminUser, recent+"&"+query)[0]
					require.Equal(t, true, listed["success"])
					assert.EqualValues(t, len(logs), listed["data"].(map[string]any)["total"])
				}
			})

			t.Run("self ignores caller filters and survives username changes", func(t *testing.T) {
				for _, username := range []string{"owner-after", "renamed-again", "owner-%", "own_er%", ""} {
					for _, injected := range []string{"user_id=84&username=42", "user_id=invalid&username=owner-%25", "user_id=84&user_id=7"} {
						response := usageLogRequest(GetLogsSelfStat, 42, username, recent+"&"+injected)
						require.Equal(t, http.StatusOK, response.Code)
						var body struct {
							Success bool       `json:"success"`
							Data    model.Stat `json:"data"`
						}
						require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
						assert.True(t, body.Success)
						assert.Equal(t, model.Stat{Quota: 30, Rpm: 1, Tpm: 3}, body.Data, username)
					}
				}
				injected := recent + "&user_id=84&username=42&channel=999"
				listed := callLogHandler(t, GetUserLogs, 42, common.RoleCommonUser, injected)[0]
				require.Equal(t, true, listed["success"])
				assert.EqualValues(t, 3, listed["data"].(map[string]any)["total"])
				assert.Equal(t, []any{"owner-error", "owner-new", "owner-old"}, exportedRequestIds(callLogHandler(t, ExportUserLogs, 42, common.RoleCommonUser, injected)))
			})
		})
	}
}

func TestUsageLogSelfRejectsMissingIdentity(t *testing.T) {
	for _, userID := range []int{0, -1} {
		for name, handler := range map[string]gin.HandlerFunc{"list": GetUserLogs, "export": ExportUserLogs, "stat": GetLogsSelfStat} {
			t.Run(name+"/"+strconv.Itoa(userID), func(t *testing.T) {
				response := usageLogRequest(handler, userID, "owner-%", "user_id=42")
				assert.Equal(t, http.StatusUnauthorized, response.Code)
				assert.NotContains(t, response.Body.String(), `"data"`)
			})
		}
	}
}

func TestUsageLogRouteAuthorizationPostgres(t *testing.T) {
	dsn := os.Getenv("AUDIT_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires disposable loopback PostgreSQL AUDIT_POSTGRES_DSN")
	}
	db, _ := newAuditTestDatabase(t, "postgres", dsn)
	useLogExportDatabases(t, db, db, common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.Channel{}, &model.Log{}))
	previousSecret := common.SessionSecret
	common.SessionSecret = "synthetic-usage-log-auth-secret"
	t.Cleanup(func() { common.SessionSecret = previousSecret })
	settings := operation_setting.GetLogSetting()
	previousExportEnabled := settings.UserExportEnabled
	settings.UserExportEnabled = true
	t.Cleanup(func() { settings.UserExportEnabled = previousExportEnabled })
	router := gin.New()
	router.GET("/api/log/", middleware.AdminAuth(), GetAllLogs)
	router.GET("/api/log/stat", middleware.AdminAuth(), GetLogsStat)
	router.GET("/api/log/export", middleware.AdminAuth(), ExportAllLogs)
	router.GET("/api/log/self", middleware.UserAuth(), GetUserLogs)
	router.GET("/api/log/self/stat", middleware.UserAuth(), GetLogsSelfStat)
	router.GET("/api/log/self/export", middleware.UserAuth(), ExportUserLogs)
	for _, role := range []int{common.RoleCommonUser, common.RoleAgentUser, common.RoleAdminUser, common.RoleRootUser} {
		t.Run(strconv.Itoa(role), func(t *testing.T) {
			user := &model.User{Username: fmt.Sprintf("usage-role-%d", role), Role: role, Status: common.UserStatusEnabled, AuthVersion: 1, AffCode: fmt.Sprintf("logrole%d", role)}
			require.NoError(t, db.Create(user).Error)
			_, token := createAccessTokenTestSession(t, user.Id, fmt.Sprintf("usage-log-%d", role))
			for _, path := range []string{"/api/log/", "/api/log/stat", "/api/log/export"} {
				request := httptest.NewRequest(http.MethodGet, path+"?user_id=42", nil)
				request.Header.Set("Authorization", "Bearer "+token)
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				if role < common.RoleAdminUser {
					assert.Equal(t, http.StatusForbidden, response.Code, path)
				} else {
					assert.Equal(t, http.StatusOK, response.Code, path)
					assert.Contains(t, response.Body.String(), `"success":true`)
				}
			}
			for _, path := range []string{"/api/log/self", "/api/log/self/stat", "/api/log/self/export"} {
				request := httptest.NewRequest(http.MethodGet, path+"?user_id=42&username=other-user", nil)
				request.Header.Set("Authorization", "Bearer "+token)
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				if role == common.RoleAgentUser {
					assert.Equal(t, http.StatusForbidden, response.Code, path)
				} else {
					assert.Equal(t, http.StatusOK, response.Code, path)
					assert.Contains(t, response.Body.String(), `"success":true`)
				}
			}
		})
	}
	for _, path := range []string{"/api/log/", "/api/log/stat", "/api/log/export", "/api/log/self", "/api/log/self/stat", "/api/log/self/export"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path+"?user_id=42", nil))
		assert.Equal(t, http.StatusUnauthorized, response.Code, path)
	}
}
