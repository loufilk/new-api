package model

import (
	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// GetDBTimestamp returns a UNIX timestamp from database time.
// Falls back to application time on error.
// 传入事务句柄时在事务连接上执行查询：调用方事务持有唯一连接
// （如 SQLite 单连接配置）时，使用全局 DB 池会永久等待空闲连接而死锁。
func GetDBTimestamp(tx ...*gorm.DB) int64 {
	db := DB
	if len(tx) > 0 && tx[0] != nil {
		db = tx[0]
	}
	var ts int64
	var err error
	switch {
	case common.UsingMainDatabase(common.DatabaseTypePostgreSQL):
		err = db.Raw("SELECT EXTRACT(EPOCH FROM NOW())::bigint").Scan(&ts).Error
	case common.UsingMainDatabase(common.DatabaseTypeSQLite):
		err = db.Raw("SELECT strftime('%s','now')").Scan(&ts).Error
	default:
		err = db.Raw("SELECT UNIX_TIMESTAMP()").Scan(&ts).Error
	}
	if err != nil || ts <= 0 {
		return common.GetTimestamp()
	}
	return ts
}
