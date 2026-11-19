package controller

import (
	"bytes"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestLogListRejectsUnboundedPagination(t *testing.T) {
	for _, handler := range []gin.HandlerFunc{GetUserLogs, GetAllLogs} {
		for _, query := range []string{"page_size=-1", "ps=-1", "size=-1", "p=-1", fmt.Sprintf("p=%d&page_size=100", math.MaxInt)} {
			r := usageLogRequest(handler, 1, "viewer", query)
			require.Equal(t, http.StatusBadRequest, r.Code, query)
		}
	}
}

func TestLogPageOptionalCountPostgres(t *testing.T) {
	dsn := os.Getenv("AUDIT_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires disposable loopback PostgreSQL")
	}
	db, _ := newAuditTestDatabase(t, "postgres", dsn)
	var statements bytes.Buffer
	logger := gormlogger.New(log.New(&statements, "", 0), gormlogger.Config{LogLevel: gormlogger.Info})
	useLogExportDatabases(t, db, db.Session(&gorm.Session{Logger: logger}), common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	require.NoError(t, db.AutoMigrate(&model.Log{}))
	now := time.Now().Unix()
	require.NoError(t, db.Exec("INSERT INTO logs(user_id,created_at,request_id) SELECT 7,?,n::text FROM generate_series(1,10003) n", now).Error)
	require.NoError(t, db.Create(&model.Log{UserId: 9, CreatedAt: now, RequestId: "foreign"}).Error)
	for _, tc := range []struct {
		name    string
		handler gin.HandlerFunc
		role    int
		filter  string
	}{
		{"admin", GetAllLogs, common.RoleAdminUser, "user_id=7"},
		{"self", GetUserLogs, common.RoleCommonUser, "user_id=9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := tc.filter + "&p=2&page_size=2"
			var countedItems []any
			for _, skip := range []bool{false, true} {
				if skip {
					query += "&skip_total=1"
				}
				statements.Reset()
				response := callLogHandler(t, tc.handler, 7, tc.role, query)[0]
				require.Equal(t, true, response["success"])
				data := response["data"].(map[string]any)
				items := data["items"].([]any)
				require.Len(t, items, 2)
				require.EqualValues(t, 7, items[0].(map[string]any)["user_id"])
				sql := strings.ToLower(statements.String())
				require.Contains(t, sql, "limit 2 offset 2")
				if !skip {
					require.Contains(t, sql, "count(")
					require.EqualValues(t, 10003, data["total"], "legacy counts remain exact above 10k")
					countedItems = items
				} else {
					require.NotContains(t, sql, "count(")
					require.EqualValues(t, -1, data["total"])
					require.Equal(t, countedItems, items)
				}
			}
		})
	}
}
