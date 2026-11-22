package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"
)

// Legacy source markers are retained solely to reject historical restricted
// codes. They are not exposed by the ordinary administrator API or rewritten.
type Redemption struct {
	AgentSimpleMode      string `json:"agent_simple_mode,omitempty" gorm:"type:varchar(16);index"`
	AgentSimpleCredit    bool   `json:"agent_simple_credit,omitempty" gorm:"not null;default:false"`
	AgentSimpleCostQuota int64  `json:"agent_simple_cost_quota,omitempty" gorm:"type:bigint;not null;default:0"`
	AgentSimplePaymentID int64  `json:"agent_simple_payment_id,omitempty" gorm:"type:bigint;not null;default:0;index"`

	CustomerUserID      int            `json:"-" gorm:"not null;default:0;index"`
	DeliveryID          int            `json:"-" gorm:"not null;default:0;index"`
	SourceProtected     bool           `json:"-" gorm:"not null;default:false"`
	BudgetAgentID       int            `json:"-" gorm:"not null;default:0;index"`
	BudgetReserved      bool           `json:"-" gorm:"not null;default:false"`
	BudgetVersion       int            `json:"-" gorm:"not null;default:0"`
	CreationOperationID int            `json:"-" gorm:"not null;default:0;index"`
	Id                  int            `json:"id"`
	UserId              int            `json:"user_id"`
	Key                 string         `json:"key" gorm:"type:char(32);uniqueIndex"`
	Status              int            `json:"status" gorm:"default:1"`
	Name                string         `json:"name" gorm:"index"`
	Quota               int            `json:"quota" gorm:"default:100"`
	CreatedTime         int64          `json:"created_time" gorm:"bigint"`
	RedeemedTime        int64          `json:"redeemed_time" gorm:"bigint"`
	Count               int            `json:"count" gorm:"-:all"`
	UsedUserId          int            `json:"used_user_id"`
	DeletedAt           gorm.DeletedAt `gorm:"index"`
	ExpiredTime         int64          `json:"expired_time" gorm:"bigint"`
}

const MaxRedemptionDeleteBatchSize = 1000

var (
	ErrRedemptionUsedImmutable    = errors.New("Used redemption codes cannot be changed or re-enabled")
	ErrRedemptionInvalidStatus    = errors.New("Invalid redemption code status")
	ErrRedemptionInvalidRequest   = errors.New("Invalid redemption code request")
	ErrRedemptionPermission       = errors.New("Unauthorized, insufficient privileges")
	ErrRedemptionRestrictedSource = errors.New("Redemption operation failed, please try again later")
)

type RedemptionCreateRequest struct {
	Name        string `json:"name"`
	Quota       int    `json:"quota"`
	Count       int    `json:"count"`
	ExpiredTime int64  `json:"expired_time"`
	RequestID   string `json:"request_id"`
}
type RedemptionUpdateRequest struct {
	Version     int64  `json:"version"`
	RequestID   string `json:"request_id"`
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Quota       int    `json:"quota"`
	Status      int    `json:"status"`
	ExpiredTime int64  `json:"expired_time"`
}

// RedemptionMutation stores non-secret identifiers for atomic create retries.
// A retry cannot create fresh value, including after a code has been deleted.
type RedemptionMutation struct {
	ID            int64  `gorm:"primaryKey"`
	UserID        int    `gorm:"not null;uniqueIndex:idx_redemption_mutation_key,priority:1"`
	RequestID     string `gorm:"type:varchar(64);not null;uniqueIndex:idx_redemption_mutation_key,priority:2"`
	Signature     string `gorm:"type:char(64);not null"`
	RedemptionIDs string `gorm:"type:text;not null"`
	CreatedAt     int64  `gorm:"autoCreateTime"`
}

func (*RedemptionMutation) BeforeUpdate(*gorm.DB) error { return ErrLedgerImmutable }
func (*RedemptionMutation) BeforeDelete(*gorm.DB) error { return ErrLedgerImmutable }

func (r *Redemption) restrictedSource() bool {
	return r.AgentSimpleMode != "" || r.AgentSimpleCredit || r.AgentSimpleCostQuota != 0 || r.AgentSimplePaymentID != 0 || r.BudgetAgentID != 0 || r.BudgetReserved || r.CustomerUserID != 0 || r.DeliveryID != 0 || r.SourceProtected
}
func ordinaryRedemptions(tx *gorm.DB) *gorm.DB {
	return tx.Where("COALESCE(agent_simple_mode, '') = '' AND agent_simple_credit = ? AND agent_simple_cost_quota = 0 AND agent_simple_payment_id = 0", false).Where("budget_agent_id = 0 AND budget_reserved = ? AND customer_user_id = 0 AND delivery_id = 0 AND source_protected = ?", false, false)
}
func validateRedemptionValue(name string, quota int, expires int64) error {
	if strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > 20 || quota <= 0 || expires < 0 {
		return ErrRedemptionInvalidRequest
	}
	return common.ValidateWalletQuota(quota)
}
func requireRedemptionAdminTx(tx *gorm.DB, identity AuthSessionIdentity) error {
	if _, token := ParseAccessTokenSessionID(identity.SessionID); token {
		return ErrRedemptionPermission
	}
	if err := ValidateAuthSessionWithTx(tx.Session(&gorm.Session{Logger: tx.Logger.LogMode(gormlogger.Silent)}), identity); err != nil {
		return ErrRedemptionPermission
	}
	var actor User
	if err := tx.Select("id", "role").First(&actor, identity.UserID).Error; err != nil {
		return err
	}
	if actor.Role != common.RoleAdminUser && actor.Role != common.RoleRootUser {
		return ErrRedemptionPermission
	}
	return nil
}
func ValidateRedemptionAdmin(identity AuthSessionIdentity) error {
	return DB.Transaction(func(tx *gorm.DB) error { return requireRedemptionAdminTx(tx, identity) })
}

func GetAllRedemptions(start, count int) ([]*Redemption, int64, error) {
	return SearchRedemptions("", "", start, count)
}
func SearchRedemptions(keyword, status string, start, count int) ([]*Redemption, int64, error) {
	start, count = min(max(start, 0), 100_000_000), min(max(count, 1), 100)
	query := ordinaryRedemptions(DB.Model(&Redemption{}))
	if keyword != "" {
		if id, err := strconv.Atoi(keyword); err == nil {
			query = query.Where("(id = ? OR name LIKE ?)", id, keyword+"%")
		} else {
			query = query.Where("name LIKE ?", keyword+"%")
		}
	}
	now := common.GetTimestamp()
	switch status {
	case "expired":
		query = query.Where("status = ? AND expired_time != 0 AND expired_time < ?", common.RedemptionCodeStatusEnabled, now)
	case strconv.Itoa(common.RedemptionCodeStatusEnabled):
		query = query.Where("status = ? AND (expired_time = 0 OR expired_time >= ?)", common.RedemptionCodeStatusEnabled, now)
	case strconv.Itoa(common.RedemptionCodeStatusDisabled):
		query = query.Where("status = ?", common.RedemptionCodeStatusDisabled)
	case strconv.Itoa(common.RedemptionCodeStatusUsed):
		query = query.Where("status = ?", common.RedemptionCodeStatusUsed)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := make([]*Redemption, 0)
	if err := query.Order("id DESC").Offset(start).Limit(count).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}
func GetRedemptionById(id int) (*Redemption, error) {
	if id <= 0 {
		return nil, ErrRedemptionInvalidRequest
	}
	var row Redemption
	err := ordinaryRedemptions(DB).First(&row, id).Error
	return &row, err
}

func CreateRedemptions(identity AuthSessionIdentity, request RedemptionCreateRequest) ([]string, error) {
	if err := validateRedemptionValue(request.Name, request.Quota, request.ExpiredTime); err != nil {
		return nil, err
	}
	if request.Count < 1 || request.Count > 100 || request.Quota > common.MaxWalletQuota/request.Count {
		return nil, ErrRedemptionInvalidRequest
	}
	if request.RequestID == "" {
		request.RequestID = common.GetUUID()
	}
	if !ValidReferralTransferIdempotencyKey(request.RequestID) {
		return nil, ErrRedemptionInvalidRequest
	}
	payload, err := common.Marshal(struct {
		Name         string
		Quota, Count int
		ExpiredTime  int64
	}{request.Name, request.Quota, request.Count, request.ExpiredTime})
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(payload)
	signature := hex.EncodeToString(digest[:])
	keys := make([]string, 0, request.Count)
	err = DB.Session(&gorm.Session{Logger: DB.Logger.LogMode(gormlogger.Silent)}).Transaction(func(tx *gorm.DB) error {
		if err := requireRedemptionAdminTx(tx, identity); err != nil {
			return err
		}
		var prior RedemptionMutation
		lookup := tx.Where("user_id = ? AND request_id = ?", identity.UserID, request.RequestID).First(&prior).Error
		if lookup == nil {
			if prior.Signature != signature {
				return ErrLedgerIdempotencyConflict
			}
			var ids []int
			if common.UnmarshalJsonStr(prior.RedemptionIDs, &ids) != nil || len(ids) != request.Count {
				return ErrRedemptionInvalidRequest
			}
			var rows []Redemption
			if err := ordinaryRedemptions(tx.Unscoped()).Where("id IN ? AND user_id = ?", ids, identity.UserID).Order("id").Find(&rows).Error; err != nil {
				return err
			}
			if len(rows) != len(ids) {
				return ErrRedemptionInvalidRequest
			}
			for _, row := range rows {
				keys = append(keys, row.Key)
			}
			return nil
		}
		if !errors.Is(lookup, gorm.ErrRecordNotFound) {
			return lookup
		}
		if request.ExpiredTime != 0 && request.ExpiredTime < common.GetTimestamp() {
			return ErrRedemptionInvalidRequest
		}
		rows := make([]Redemption, 0, request.Count)
		for range request.Count {
			key := common.GetUUID()
			rows = append(rows, Redemption{UserId: identity.UserID, Key: key, Name: request.Name, Quota: request.Quota, Status: common.RedemptionCodeStatusEnabled, CreatedTime: common.GetTimestamp(), ExpiredTime: request.ExpiredTime})
			keys = append(keys, key)
		}
		if err := tx.Create(&rows).Error; err != nil {
			return err
		}
		ids := make([]int, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.Id)
		}
		rawIDs, err := common.Marshal(ids)
		if err != nil {
			return err
		}
		return tx.Create(&RedemptionMutation{UserID: identity.UserID, RequestID: request.RequestID, Signature: signature, RedemptionIDs: string(rawIDs)}).Error
	})
	if err != nil {
		return nil, err
	}
	return keys, nil
}

func Redeem(key string, userID int) (int, error) {
	if key == "" || userID <= 0 {
		return 0, ErrRedeemFailed
	}
	var redemption Redemption
	var totalAfter int
	expired := false
	err := DB.Session(&gorm.Session{Logger: DB.Logger.LogMode(gormlogger.Silent)}).Transaction(func(tx *gorm.DB) error {
		// SQL predicates also reject NULL legacy markers before GORM can scan
		// an unknown source as a zero-valued ordinary code.
		if err := lockForUpdate(tx).Where(clause.Eq{Column: clause.Column{Name: "key"}, Value: key}).First(&redemption).Error; err != nil {
			return err
		}
		if redemption.AgentSimpleMode == "" {
			// Keep SQL-level provenance checks: NULL historical markers are not ordinary.
			var ordinary int64
			if err := ordinaryRedemptions(tx.Model(&Redemption{})).Where("id = ?", redemption.Id).Count(&ordinary).Error; err != nil {
				return err
			}
			if ordinary != 1 || redemption.restrictedSource() {
				return ErrRedemptionRestrictedSource
			}
		} else {
			if redemption.BudgetAgentID <= 0 || redemption.DeliveryID != 0 || redemption.SourceProtected || (redemption.CustomerUserID != 0 && redemption.CustomerUserID != userID) {
				return ErrRedemptionRestrictedSource
			}
			switch redemption.AgentSimpleMode {
			case AgentSimpleCodeDirect, AgentSimpleCodeCredit, AgentSimpleCodePayment:
			default:
				return ErrRedemptionRestrictedSource
			}
		}
		if redemption.Status != common.RedemptionCodeStatusEnabled || redemption.UsedUserId != 0 || redemption.RedeemedTime != 0 {
			return ErrRedemptionUsedImmutable
		}
		if redemption.ExpiredTime != 0 && redemption.ExpiredTime < common.GetTimestamp() {
			if redemption.AgentSimpleMode != "" && redemption.AgentSimpleMode != AgentSimpleCodePayment && redemption.BudgetReserved {
				if err := ExpireAgentSimpleCodeTx(tx, &redemption, 0); err != nil {
					return err
				}
				expired = true
				return tx.Model(&redemption).Updates(map[string]any{"status": common.RedemptionCodeStatusDisabled, "budget_reserved": redemption.BudgetReserved, "budget_version": redemption.BudgetVersion}).Error
			}
			return ErrRedemptionInvalidRequest
		}
		if redemption.Quota <= 0 || common.ValidateWalletQuota(redemption.Quota) != nil {
			return ErrRedemptionInvalidRequest
		}
		if redemption.AgentSimpleMode != "" {
			if !redemption.BudgetReserved {
				return ErrAgentSimpleInvariant
			}
			if redemption.BudgetReserved {
				if err := RedeemAgentSimpleCodeTx(tx, &redemption, userID); err != nil {
					return err
				}
			}
		}
		result := tx.Model(&Redemption{}).Where("id = ? AND status = ? AND used_user_id = 0 AND redeemed_time = 0", redemption.Id, common.RedemptionCodeStatusEnabled).Updates(map[string]any{"redeemed_time": common.GetTimestamp(), "status": common.RedemptionCodeStatusUsed, "used_user_id": userID, "budget_reserved": redemption.BudgetReserved, "budget_version": redemption.BudgetVersion})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrRedemptionUsedImmutable
		}
		ledger, err := CreditQuotaTx(tx, LedgerEntry{UserId: userID, Account: AccountPaid, Delta: redemption.Quota, Reason: LedgerReasonRedemption, ReferenceType: "redemption", ReferenceId: strconv.Itoa(redemption.Id)})
		if err != nil {
			return err
		}
		if ledger.Replayed {
			return ErrRedemptionUsedImmutable
		}
		totalAfter = ledger.TotalAfter
		return nil
	})
	if err != nil {
		common.SysError("redemption failed") // Never log a usable code or SQL diagnostic.
		return 0, ErrRedeemFailed
	}
	if expired {
		return 0, ErrRedeemFailed
	}
	RefreshUserQuotaCache(userID, totalAfter)
	RecordLog(userID, LogTypeTopup, common.NewMessage("Topped up {{quota}} with a redemption code, code ID {{id}}", map[string]any{"quota": logger.FormatQuota(redemption.Quota), "id": redemption.Id}))
	return redemption.Quota, nil
}

func (r *Redemption) Insert() error {
	if r.restrictedSource() {
		return ErrRedemptionRestrictedSource
	}
	if r.Quota <= 0 {
		return ErrRedemptionInvalidRequest
	}
	if err := common.ValidateWalletQuota(r.Quota); err != nil {
		return err
	}
	return DB.Session(&gorm.Session{Logger: DB.Logger.LogMode(gormlogger.Silent)}).Create(r).Error
}

// HTTP callers always pass the authoritative browser identity. Internal callers
// may omit it; use markers and restricted provenance remain non-bypassable.
func UpdateRedemption(request RedemptionUpdateRequest, statusOnly bool, identities ...AuthSessionIdentity) (*Redemption, error) {
	if request.ID <= 0 {
		return nil, ErrRedemptionInvalidRequest
	}
	if statusOnly {
		if request.Status != common.RedemptionCodeStatusEnabled && request.Status != common.RedemptionCodeStatusDisabled {
			return nil, ErrRedemptionInvalidStatus
		}
	} else if err := validateRedemptionValue(request.Name, request.Quota, request.ExpiredTime); err != nil {
		return nil, err
	}
	var row Redemption
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).First(&row, request.ID).Error; err != nil {
			return err
		}
		// Code -> user is the same lock order as redemption into a wallet.
		if len(identities) > 0 {
			if len(identities) != 1 {
				return ErrRedemptionPermission
			}
			if err := requireRedemptionAdminTx(tx, identities[0]); err != nil {
				return err
			}
		}
		if row.restrictedSource() {
			return ErrRedemptionRestrictedSource
		}
		if (row.Status != common.RedemptionCodeStatusEnabled && row.Status != common.RedemptionCodeStatusDisabled) || row.UsedUserId != 0 || row.RedeemedTime != 0 {
			return ErrRedemptionUsedImmutable
		}
		if statusOnly {
			row.Status = request.Status
		} else {
			row.Name, row.Quota, row.ExpiredTime = request.Name, request.Quota, request.ExpiredTime
		}
		if row.Status == common.RedemptionCodeStatusEnabled && row.ExpiredTime != 0 && row.ExpiredTime < common.GetTimestamp() {
			return ErrRedemptionInvalidRequest
		}
		return tx.Model(&row).Select("name", "quota", "status", "expired_time").Updates(&row).Error
	})
	return &row, err
}
func (r *Redemption) SelectUpdate() error {
	updated, err := UpdateRedemption(RedemptionUpdateRequest{ID: r.Id, Status: r.Status}, true)
	if err == nil {
		*r = *updated
	}
	return err
}
func (r *Redemption) Update() error {
	updated, err := UpdateRedemption(RedemptionUpdateRequest{ID: r.Id, Name: r.Name, Quota: r.Quota, ExpiredTime: r.ExpiredTime}, false)
	if err == nil {
		*r = *updated
	}
	return err
}
func DeleteRedemptions(ids []int, invalidOnly bool, identities ...AuthSessionIdentity) (int64, error) {
	if !invalidOnly && (len(ids) == 0 || len(ids) > MaxRedemptionDeleteBatchSize) {
		return 0, ErrRedemptionInvalidRequest
	}
	for _, id := range ids {
		if id <= 0 {
			return 0, ErrRedemptionInvalidRequest
		}
	}
	var deleted int64
	err := DB.Transaction(func(tx *gorm.DB) error {
		query := tx.Model(&Redemption{})
		if invalidOnly {
			query = ordinaryRedemptions(query).Where("(status IN ? OR (status = ? AND expired_time != 0 AND expired_time < ?))", []int{common.RedemptionCodeStatusUsed, common.RedemptionCodeStatusDisabled}, common.RedemptionCodeStatusEnabled, common.GetTimestamp()).Limit(MaxRedemptionDeleteBatchSize)
		} else {
			query = query.Where("id IN ?", ids)
		}
		var rows []Redemption
		if err := lockForUpdate(query).Order("id").Find(&rows).Error; err != nil {
			return err
		}
		if len(identities) > 0 {
			if len(identities) != 1 {
				return ErrRedemptionPermission
			}
			if err := requireRedemptionAdminTx(tx, identities[0]); err != nil {
				return err
			}
		}
		for _, row := range rows {
			if row.restrictedSource() {
				return ErrRedemptionRestrictedSource
			}
		}
		for i := range rows {
			result := tx.Delete(&rows[i])
			if result.Error != nil {
				return result.Error
			}
			deleted += result.RowsAffected
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}
func (r *Redemption) Delete() error                   { return DeleteRedemptionById(r.Id) }
func DeleteRedemptionById(id int) error               { _, err := DeleteRedemptions([]int{id}, false); return err }
func DeleteInvalidRedemptions() (int64, error)        { return DeleteRedemptions(nil, true) }
func BatchDeleteRedemptions(ids []int) (int64, error) { return DeleteRedemptions(ids, false) }
