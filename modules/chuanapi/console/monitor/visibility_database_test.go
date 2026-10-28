package monitor

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func requestMonitorSettings(router *gin.Engine, method, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, "/api/option/model-monitor", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	return recorder
}

func seedMonitorSettings(t *testing.T, db *gorm.DB, raw string) {
	t.Helper()
	require.NoError(t, db.Where(&model.Option{Key: settingsOption}).Delete(&model.Option{}).Error)
	if raw != "" {
		require.NoError(t, db.Create(&model.Option{Key: settingsOption, Value: raw}).Error)
	}
	model.InitOptionMap()
}

// Only the fixture's uniquely prefixed options table is created or removed.
// Missing MySQL/PostgreSQL services are explicit skips, not compatibility passes.
func TestMonitorHiddenGroupsDatabaseMatrix(t *testing.T) {
	for _, dialect := range []common.DatabaseType{common.DatabaseTypeSQLite, common.DatabaseTypeMySQL, common.DatabaseTypePostgreSQL} {
		t.Run(string(dialect), func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case common.DatabaseTypeSQLite:
				driver = sqlite.Open(filepath.Join(t.TempDir(), "monitor.db"))
			case common.DatabaseTypeMySQL, common.DatabaseTypePostgreSQL:
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dialect == common.DatabaseTypePostgreSQL {
					dsn = os.Getenv("TEST_POSTGRES_DSN")
				}
				if dsn == "" && os.Getenv("TEST_DATABASE_DIALECT") == string(dialect) {
					dsn = os.Getenv("TEST_DATABASE_DSN")
				}
				if dsn == "" {
					if os.Getenv("TEST_DATABASE_COMPAT") == "1" && os.Getenv("TEST_DATABASE_DIALECT") == string(dialect) {
						t.Fatal("selected real database requires a disposable fixture DSN")
					}
					t.Skip("no disposable " + string(dialect) + " fixture configured")
				}
				if dialect == common.DatabaseTypeMySQL {
					driver = mysql.Open(dsn)
				} else {
					driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
				}
			}
			gormConfig := &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: fmt.Sprintf("monitor_%x_", time.Now().UnixNano())}}
			db, err := gorm.Open(driver, gormConfig)
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			previousDB, previousLogDB := model.DB, model.LOG_DB
			previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
			previousRedis := common.RedisEnabled
			model.DB, model.LOG_DB = db, db
			common.SetDatabaseTypes(dialect, dialect)
			common.RedisEnabled = false
			monitorOptionFixture(t, "")
			t.Cleanup(func() {
				model.DB, model.LOG_DB = previousDB, previousLogDB
				common.SetDatabaseTypes(previousMain, previousLog)
				common.RedisEnabled = previousRedis
				require.NoError(t, sqlDB.Close())
			})
			require.False(t, db.Migrator().HasTable(&model.Option{}), "fixture must not reuse an application table")
			require.NoError(t, db.AutoMigrate(&model.Option{}))
			t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(&model.Option{})) })
			versionQuery := "SELECT version()"
			if dialect == common.DatabaseTypeSQLite {
				versionQuery = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
			t.Logf("%s version: %s", dialect, version)
			gin.SetMode(gin.TestMode)
			router := gin.New()
			s := New(false)
			s.RegisterSettings(router.Group("/api/option"))
			id := "route-" + strings.Repeat("a", 32)
			otherID := "route-" + strings.Repeat("b", 32)
			original := fmt.Sprintf(`{"enabled":true,"title":"Production","description":"Saved before upgrade","groups":{%q:"Private group"},"hidden_groups":{%q:true}}`, id, id)

			t.Run("production_json_reload", func(t *testing.T) {
				seedMonitorSettings(t, db, original)
				for range 2 {
					model.InitOptionMap()
					response := requestMonitorSettings(router, http.MethodGet, "")
					require.Equal(t, http.StatusOK, response.Code)
					var body struct {
						Data struct {
							Config common.RawMessage `json:"config"`
						} `json:"data"`
					}
					require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
					assert.JSONEq(t, original, string(body.Data.Config))
				}
				var stored model.Option
				require.NoError(t, db.First(&stored, &model.Option{Key: settingsOption}).Error)
				assert.JSONEq(t, original, stored.Value, "loading existing settings is read-only")
			})

			t.Run("save_contract", func(t *testing.T) {
				tooMany := make(map[string]bool)
				for i := range 129 {
					tooMany[fmt.Sprintf("route-%032x", i)] = true
				}
				oversized, err := common.Marshal(tooMany)
				require.NoError(t, err)
				for _, tc := range []struct {
					name, field, expectedHidden string
					invalid                     bool
				}{
					{"omitted_preserves", "", fmt.Sprintf(`{%q:true}`, id), false},
					{"null_preserves", `,"hidden_groups":null`, fmt.Sprintf(`{%q:true}`, id), false},
					{"empty_clears", `,"hidden_groups":{}`, `{}`, false},
					{"false_clears", fmt.Sprintf(`,"hidden_groups":{%q:false}`, id), `{}`, false},
					{"explicit_replaces", fmt.Sprintf(`,"hidden_groups":{%q:true}`, otherID), fmt.Sprintf(`{%q:true}`, otherID), false},
					{"invalid_key_rejected", `,"hidden_groups":{"route-1":true}`, "", true},
					{"invalid_false_key_rejected", `,"hidden_groups":{"route-1":false}`, "", true},
					{"invalid_type_rejected", fmt.Sprintf(`,"hidden_groups":{%q:"true"}`, id), "", true},
					{"too_many_rejected", `,"hidden_groups":` + string(oversized), "", true},
				} {
					t.Run(tc.name, func(t *testing.T) {
						seedMonitorSettings(t, db, original)
						request := `{"enabled":false,"title":" Updated ","description":" New description ","groups":{}` + tc.field + `}`
						response := requestMonitorSettings(router, http.MethodPut, request)
						expected := original
						if tc.invalid {
							assert.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
							assert.Contains(t, response.Body.String(), "monitor.settings.invalid")
						} else {
							require.Equal(t, http.StatusOK, response.Code, response.Body.String())
							expected = `{"enabled":false,"title":"Updated","description":"New description","groups":{},"hidden_groups":` + tc.expectedHidden + `}`
							var body envelope[common.RawMessage]
							require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
							assert.JSONEq(t, expected, string(body.Data))
						}
						var stored model.Option
						require.NoError(t, db.First(&stored, &model.Option{Key: settingsOption}).Error)
						assert.JSONEq(t, expected, stored.Value)
						model.InitOptionMap()
						loaded, err := readConfig()
						require.NoError(t, err)
						encoded, err := common.Marshal(loaded)
						require.NoError(t, err)
						assert.JSONEq(t, expected, string(encoded))
					})
				}
			})

			t.Run("fresh_default_insert", func(t *testing.T) {
				seedMonitorSettings(t, db, "")
				response := requestMonitorSettings(router, http.MethodPut, `{"enabled":true,"title":"New","description":"","groups":{}}`)
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				var stored model.Option
				require.NoError(t, db.First(&stored, &model.Option{Key: settingsOption}).Error)
				assert.JSONEq(t, `{"enabled":true,"title":"New","description":"","groups":{},"hidden_groups":{}}`, stored.Value)
			})

			t.Run("corrupt_saved_visibility_fails_closed", func(t *testing.T) {
				corrupt := `{"enabled":true,"title":"","description":"","groups":{},"hidden_groups":{"bad":true}}`
				seedMonitorSettings(t, db, corrupt)
				assert.Equal(t, http.StatusServiceUnavailable, requestMonitorSettings(router, http.MethodGet, "").Code)
				response := requestMonitorSettings(router, http.MethodPut, `{"enabled":true,"title":"Legacy write","description":"","groups":{}}`)
				assert.Equal(t, http.StatusServiceUnavailable, response.Code)
				var stored model.Option
				require.NoError(t, db.First(&stored, &model.Option{Key: settingsOption}).Error)
				assert.JSONEq(t, corrupt, stored.Value)
				// An explicit valid visibility replacement can repair corrupt settings.
				response = requestMonitorSettings(router, http.MethodPut, `{"enabled":true,"title":"Repaired","description":"","groups":{},"hidden_groups":{}}`)
				assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
			})

			t.Run("failed_write_preserves_saved_visibility", func(t *testing.T) {
				seedMonitorSettings(t, db, original)
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("monitor_visibility_failure", func(tx *gorm.DB) {
					tx.AddError(errors.New("injected option write failure"))
				}))
				t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove("monitor_visibility_failure")) })
				response := requestMonitorSettings(router, http.MethodPut, `{"enabled":true,"title":"Changed","description":"","groups":{},"hidden_groups":{}}`)
				assert.Equal(t, http.StatusInternalServerError, response.Code)
				loaded, err := readConfig()
				require.NoError(t, err)
				encoded, err := common.Marshal(loaded)
				require.NoError(t, err)
				assert.JSONEq(t, original, string(encoded))
				var stored model.Option
				require.NoError(t, db.First(&stored, &model.Option{Key: settingsOption}).Error)
				assert.JSONEq(t, original, stored.Value)
			})

			t.Run("concurrent_legacy_write_preserves_visibility", func(t *testing.T) {
				seedMonitorSettings(t, db, original)
				start := make(chan struct{})
				responses := make(chan *httptest.ResponseRecorder, 2)
				for _, body := range []string{
					`{"enabled":true,"title":"Old client","description":"","groups":{}}`,
					fmt.Sprintf(`{"enabled":true,"title":"New client","description":"","groups":{},"hidden_groups":{%q:true}}`, otherID),
				} {
					go func() {
						<-start
						responses <- requestMonitorSettings(router, http.MethodPut, body)
					}()
				}
				close(start)
				for range 2 {
					response := <-responses
					assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
				}
				var stored model.Option
				require.NoError(t, db.First(&stored, &model.Option{Key: settingsOption}).Error)
				var persisted struct {
					HiddenGroups map[string]bool `json:"hidden_groups"`
				}
				require.NoError(t, common.UnmarshalJsonStr(stored.Value, &persisted))
				assert.Equal(t, map[string]bool{otherID: true}, persisted.HiddenGroups, "either serialization order must retain the explicit visibility update")
				loaded, err := readConfig()
				require.NoError(t, err)
				encoded, err := common.Marshal(loaded)
				require.NoError(t, err)
				assert.JSONEq(t, stored.Value, string(encoded))
			})

			t.Run("connection_reopen_preserves_visibility", func(t *testing.T) {
				seedMonitorSettings(t, db, original)
				response := requestMonitorSettings(router, http.MethodPut, `{"enabled":true,"title":"After upgrade","description":"","groups":{}}`)
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				for range 2 {
					require.NoError(t, sqlDB.Close())
					db, err = gorm.Open(driver, gormConfig)
					require.NoError(t, err)
					sqlDB, err = db.DB()
					require.NoError(t, err)
					sqlDB.SetMaxOpenConns(1)
					model.DB, model.LOG_DB = db, db
					model.InitOptionMap()
					loaded, err := readConfig()
					require.NoError(t, err)
					encoded, err := common.Marshal(loaded)
					require.NoError(t, err)
					assert.JSONEq(t, fmt.Sprintf(`{"enabled":true,"title":"After upgrade","description":"","groups":{},"hidden_groups":{%q:true}}`, id), string(encoded))
				}
			})
		})
	}
}
