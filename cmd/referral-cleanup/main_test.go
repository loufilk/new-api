package main

import (
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/require"
)

// TestOpenDatabaseSkipsStartupMigrations 锁定命令层回归：inspect 默认路径打开
// 连接时不得执行 master 迁移。openDatabase 必须在连接前关闭 IsMasterNode，
// 否则 model.InitDB 的 AutoMigrate 会创建/修改表。
func TestOpenDatabaseSkipsStartupMigrations(t *testing.T) {
	previousDB := model.DB
	previousIsMaster := common.IsMasterNode
	previousSQLitePath := common.SQLitePath
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	t.Cleanup(func() {
		model.DB = previousDB
		common.IsMasterNode = previousIsMaster
		common.SQLitePath = previousSQLitePath
		common.SetDatabaseTypes(previousMainType, previousLogType)
	})

	t.Setenv("SQL_DSN", "")
	common.SQLitePath = filepath.Join(t.TempDir(), "referral-inspect.db")
	common.IsMasterNode = true

	require.NoError(t, openDatabase(), "inspect 打开连接必须成功")
	require.False(t, common.IsMasterNode, "openDatabase 必须在连接前关闭 master 迁移")

	var tables int64
	require.NoError(t, model.DB.Raw(
		"SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'",
	).Scan(&tables).Error)
	require.Zero(t, tables, "inspect 打开连接不得创建任何表（尤其不得运行 AutoMigrate）")

	closeDatabase()
}
