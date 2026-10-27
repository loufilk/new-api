package model

import (
	"context"
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// RecordTaskBillingLogTx joins the financial transaction only when the main
// and log handles actually share the same SQL connection pool. Same DSN or
// dialect is not evidence of transaction participation. A false result asks
// the caller for a best-effort display write after a confirmed commit; separate
// log databases (including ClickHouse) are not authoritative financial state.
func RecordTaskBillingLogTx(tx *gorm.DB, params RecordTaskBillingLogParams) (bool, error) {
	if tx == nil {
		return false, errors.New("missing task financial transaction")
	}
	if params.LogType == LogTypeConsume && !common.LogConsumeEnabled {
		return true, nil
	}
	if DB == nil || LOG_DB == nil || common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		return false, nil
	}
	mainPool, mainErr := DB.DB()
	logPool, logErr := LOG_DB.DB()
	if mainErr != nil || logErr != nil || mainPool != logPool {
		return false, nil
	}
	var user User
	if err := tx.Select("id", "username").First(&user, params.UserId).Error; err != nil {
		return false, err
	}
	tokenName := ""
	if params.TokenId > 0 {
		var token Token
		err := tx.Unscoped().Select("id", "name").First(&token, params.TokenId).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return false, err
		}
		tokenName = token.Name
	}
	other := params.Other
	if other == nil {
		other = NewLogOther()
	}
	if params.OperationID != "" {
		other.SetAdmin("financial_operation_id", params.OperationID)
	}
	log := &Log{
		UserId: params.UserId, Username: user.Username, CreatedAt: common.GetTimestamp(),
		Type: params.LogType, Content: other.setContent(params.Content), TokenName: tokenName,
		ModelName: params.ModelName, Quota: params.Quota, ChannelId: params.ChannelId,
		TokenId: params.TokenId, Group: params.Group, Other: other.JSONString(),
	}
	if params.OperationID != "" {
		log.RequestId = BillingTokenFingerprint(params.OperationID)
	}
	ensureLogRequestId(log)
	return true, tx.Create(log).Error
}

// RecordTaskQuotaExport preserves the ordinary task dashboard projection when
// its log already joined the main transaction. This is best effort, once per
// acknowledged new operation; never evidence for money or a replay trigger.
func RecordTaskQuotaExport(parent context.Context, params RecordTaskBillingLogParams) {
	if params.LogType != LogTypeConsume || !common.LogConsumeEnabled || !common.DataExportEnabled {
		return
	}
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	var user User
	if err := DB.WithContext(ctx).Select("id", "username").First(&user, params.UserId).Error; err != nil {
		return
	}
	node := params.NodeName
	if node == "" {
		node = common.NodeName
	}
	LogQuotaData(QuotaDataLogParams{UserID: params.UserId, Username: user.Username, ModelName: params.ModelName,
		Quota: params.Quota, CreatedAt: common.GetTimestamp(), UseGroup: params.Group, TokenID: params.TokenId, ChannelID: params.ChannelId, NodeName: node})
}
