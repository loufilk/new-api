package model

import (
	"errors"
	"fmt"
	"math/big"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	AgentFundPurchase       = "purchase"
	AgentFundCommission     = "commission_transfer"
	AgentFundLegacy         = "legacy"
	AgentFundPromotion      = "promotion"
	AgentSliceAvailable     = "available"
	AgentSliceReserved      = "reserved"
	AgentSliceSpent         = "spent"
	AgentSliceRefundFrozen  = "refund_frozen"
	AgentSliceRefunded      = "refunded"
	AgentSliceReclaimed     = "reclaimed"
	AgentSliceReclassified  = "reclassified"
	AgentSliceReclassFrozen = "reclass_frozen"
)

var ErrAgentFundInvariant = errors.New("Agent funding source reconciliation failed")

// A batch is immutable provenance, not a second balance. Its slices partition
// [0, OriginalQuota); the existing four-counter account remains authoritative.
type AgentFundBatch struct {
	PaymentReceiptID  int64         `json:"payment_receipt_id" gorm:"index"`
	ID                int           `json:"id" gorm:"primaryKey"`
	SourceKey         string        `json:"source_key" gorm:"type:varchar(128);not null;uniqueIndex"`
	AgentUserID       int           `json:"agent_user_id" gorm:"not null;index"`
	SourceKind        string        `json:"source_kind" gorm:"type:varchar(32);not null"`
	PurchaseOrderID   int           `json:"purchase_order_id" gorm:"index"`
	OriginalQuota     int           `json:"original_quota" gorm:"type:bigint;not null"`
	OriginalPaidMinor string        `json:"original_paid_minor" gorm:"type:varchar(32);not null"`
	Currency          string        `json:"currency" gorm:"type:varchar(8);not null"`
	CurrencyExponent  int           `json:"currency_exponent" gorm:"not null"`
	WalletAccount     LedgerAccount `json:"wallet_account" gorm:"type:varchar(16);not null"`
	CashRefundable    bool          `json:"cash_refundable" gorm:"not null"`
	PolicyVersion     int64         `json:"policy_version" gorm:"not null"`
	PolicySnapshot    string        `json:"-" gorm:"type:text;not null"`
	CreatedAt         int64         `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentFundBatch) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentFundBatch) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

type AgentFundSlice struct {
	ReclassificationID int    `json:"reclassification_id" gorm:"index"`
	ID                 int    `json:"id" gorm:"primaryKey"`
	BatchID            int    `json:"batch_id" gorm:"not null;uniqueIndex:idx_agent_fund_slice_interval,priority:1;index"`
	AgentUserID        int    `json:"agent_user_id" gorm:"not null;index"`
	StartQuota         int    `json:"start_quota" gorm:"type:bigint;not null;uniqueIndex:idx_agent_fund_slice_interval,priority:2"`
	Quota              int    `json:"quota" gorm:"type:bigint;not null"`
	State              string `json:"state" gorm:"type:varchar(24);not null;index"`
	RedemptionID       int    `json:"redemption_id" gorm:"index"`
	RefundID           int    `json:"refund_id" gorm:"index"`
	UpdatedAt          int64  `json:"updated_at" gorm:"autoUpdateTime"`
}

type AgentFundLedger struct {
	Currency           string `json:"currency" gorm:"type:varchar(3)"`
	CurrencyExponent   int    `json:"currency_exponent"`
	ReclassificationID int    `json:"reclassification_id" gorm:"index"`
	ID                 int    `json:"id" gorm:"primaryKey"`
	EventKey           string `json:"-" gorm:"type:varchar(160);not null;uniqueIndex"`
	AgentUserID        int    `json:"agent_user_id" gorm:"not null;index"`
	BatchID            int    `json:"batch_id" gorm:"not null;index"`
	StartQuota         int    `json:"start_quota" gorm:"type:bigint;not null"`
	Quota              int    `json:"quota" gorm:"type:bigint;not null"`
	CostMinor          string `json:"cost_minor" gorm:"type:varchar(32);not null"`
	FromState          string `json:"from_state" gorm:"type:varchar(24);not null"`
	ToState            string `json:"to_state" gorm:"type:varchar(24);not null"`
	RedemptionID       int    `json:"redemption_id" gorm:"index"`
	RefundID           int    `json:"refund_id" gorm:"index"`
	CreatedAt          int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentFundLedger) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentFundLedger) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

type AgentFundAnomaly struct {
	ID          int    `json:"id" gorm:"primaryKey"`
	AgentUserID int    `json:"agent_user_id" gorm:"not null;uniqueIndex:idx_agent_fund_anomaly,priority:1"`
	Code        string `json:"code" gorm:"type:varchar(64);not null;uniqueIndex:idx_agent_fund_anomaly,priority:2"`
	Detail      string `json:"detail" gorm:"type:varchar(255);not null"`
	CreatedAt   int64  `json:"created_at" gorm:"autoCreateTime"`
}

// Interval differences retain immutable provenance, not economic refund/AR
// authority. Actual economics use cumulative spent/released carry instead.
func AgentFundSliceCost(batch *AgentFundBatch, start, quota int) (int64, error) {
	paid, ok := new(big.Int).SetString(batch.OriginalPaidMinor, 10)
	if !ok || paid.Sign() < 0 || !paid.IsInt64() || paid.Int64() > int64(common.MaxWalletQuota) || paid.String() != batch.OriginalPaidMinor || batch.OriginalQuota <= 0 || batch.OriginalQuota > common.MaxWalletQuota || start < 0 || quota <= 0 || start > batch.OriginalQuota || quota > batch.OriginalQuota-start {
		return 0, ErrAgentFundInvariant
	}
	denominator := big.NewInt(int64(batch.OriginalQuota))
	endCost := new(big.Int).Quo(new(big.Int).Mul(paid, big.NewInt(int64(start+quota))), denominator)
	startCost := new(big.Int).Quo(new(big.Int).Mul(paid, big.NewInt(int64(start))), denominator)
	return endCost.Sub(endCost, startCost).Int64(), nil
}

func appendAgentFundLedgerTx(tx *gorm.DB, batch *AgentFundBatch, slice *AgentFundSlice, from, operation string) (int64, error) {
	book, err := AgentFundSliceCost(batch, slice.StartQuota, slice.Quota)
	if err != nil {
		return 0, err
	}
	actual := book
	if from != "" {
		actual, err = applyAgentFundEconomicTransitionTx(tx, batch, slice, from, operation)
		if err != nil {
			return 0, err
		}
		if err := applyAgentCreditSourceTransitionTx(tx, batch, slice, from, operation, actual); err != nil {
			return 0, err
		}
	}
	err = tx.Create(&AgentFundLedger{EventKey: fmt.Sprintf("%s:%d:%d", operation, batch.ID, slice.StartQuota), AgentUserID: batch.AgentUserID, Currency: batch.Currency, CurrencyExponent: batch.CurrencyExponent, BatchID: batch.ID, StartQuota: slice.StartQuota, Quota: slice.Quota, CostMinor: strconv.FormatInt(book, 10), FromState: from, ToState: slice.State, RedemptionID: slice.RedemptionID, RefundID: slice.RefundID, ReclassificationID: slice.ReclassificationID}).Error
	return actual, err
}

func createAgentFundBatchTx(tx *gorm.DB, batch *AgentFundBatch) error {
	if batch.AgentUserID <= 0 || batch.OriginalQuota <= 0 || batch.OriginalQuota > common.MaxWalletQuota || batch.SourceKey == "" {
		return ErrAgentQuotaInvalid
	}
	if _, err := AgentFundSliceCost(batch, 0, batch.OriginalQuota); err != nil {
		return err
	}
	switch batch.SourceKind {
	case AgentFundPurchase:
		if batch.OriginalPaidMinor == "0" || batch.PaymentReceiptID <= 0 || batch.WalletAccount != AccountPaid || !validAgentCurrency(batch.Currency, batch.CurrencyExponent) {
			return ErrAgentFundInvariant
		}
	case AgentFundCredit:
		if batch.CashRefundable || batch.PaymentReceiptID != 0 || batch.WalletAccount != AccountReward || batch.OriginalPaidMinor == "0" || !validAgentCurrency(batch.Currency, batch.CurrencyExponent) {
			return ErrAgentFundInvariant
		}
	case AgentFundCommission:
		if batch.CashRefundable || batch.WalletAccount != AccountReward || batch.OriginalPaidMinor == "0" || !validAgentCurrency(batch.Currency, batch.CurrencyExponent) {
			return ErrAgentFundInvariant
		}
	case AgentFundLegacy, AgentFundPromotion:
		if batch.CashRefundable || batch.OriginalPaidMinor != "0" {
			return ErrAgentFundInvariant
		}
	default:
		return ErrAgentFundInvariant
	}

	if err := tx.Create(batch).Error; err != nil {
		return err
	}
	slice := AgentFundSlice{BatchID: batch.ID, AgentUserID: batch.AgentUserID, Quota: batch.OriginalQuota, State: AgentSliceAvailable}
	if err := tx.Create(&slice).Error; err != nil {
		return err
	}
	if _, err := ensureAgentFundEconomicsTx(tx, batch); err != nil {
		return err
	}
	_, err := appendAgentFundLedgerTx(tx, batch, &slice, "", batch.SourceKey+":create")
	return err
}

// ensureAgentFundSourcesTx only supplements previously unsourced budgets. It
// does not credit any wallet or aggregate. Suspicious data is never normalized.
func ensureAgentFundSourcesTx(tx *gorm.DB, account *AgentRedemptionAccount) error {
	var count int64
	if err := tx.Model(&AgentFundBatch{}).Where("agent_user_id = ?", account.AgentUserID).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 || account.AllocatedQuota == 0 {
		return nil
	}
	if err := validateAgentAccount(account); err != nil {
		return err
	}
	var codes []Redemption
	if err := tx.Unscoped().Where("budget_agent_id = ? AND budget_reserved = ?", account.AgentUserID, true).Order("id ASC").Find(&codes).Error; err != nil {
		return err
	}
	reserved := 0
	for _, code := range codes {
		if code.Quota <= 0 || code.Quota > account.ReservedQuota-reserved || code.UsedUserId != 0 || code.RedeemedTime != 0 || code.Status == common.RedemptionCodeStatusUsed || code.DeletedAt.Valid {
			return ErrAgentFundInvariant
		}
		reserved += code.Quota
	}
	if reserved != account.ReservedQuota {
		return ErrAgentFundInvariant
	}
	batch := AgentFundBatch{SourceKey: fmt.Sprintf("legacy:%d", account.AgentUserID), AgentUserID: account.AgentUserID, SourceKind: AgentFundLegacy, OriginalQuota: account.AllocatedQuota, OriginalPaidMinor: "0", WalletAccount: AccountPaid, PolicySnapshot: "{}"}
	if err := tx.Create(&batch).Error; err != nil {
		return err
	}
	start := 0
	segments := make([]AgentFundSlice, 0, len(codes)+2)
	if account.AvailableQuota > 0 {
		segments = append(segments, AgentFundSlice{Quota: account.AvailableQuota, State: AgentSliceAvailable})
	}
	for _, code := range codes {
		segments = append(segments, AgentFundSlice{Quota: code.Quota, State: AgentSliceReserved, RedemptionID: code.Id})
	}
	if account.SpentQuota > 0 {
		segments = append(segments, AgentFundSlice{Quota: account.SpentQuota, State: AgentSliceSpent})
	}
	for i := range segments {
		slice := &segments[i]
		slice.BatchID = batch.ID
		slice.AgentUserID = account.AgentUserID
		slice.StartQuota = start
		start += slice.Quota
		if err := tx.Create(slice).Error; err != nil {
			return err
		}
		if _, err := appendAgentFundLedgerTx(tx, &batch, slice, "", batch.SourceKey+":migrate"); err != nil {
			return err
		}
	}
	return nil
}

// takeAgentFundSlicesTx requires the aggregate account lock. The caller updates
// the aggregate through applyAgentQuotaChangeTx in the same transaction.
func takeAgentFundSlicesTx(tx *gorm.DB, owner, quota, batchID int, kinds []string, state, operation string, redemptionID, refundID int, reclassificationID ...int) ([]AgentFundSlice, error) {
	if quota <= 0 || quota > common.MaxWalletQuota {
		return nil, ErrAgentQuotaInvalid
	}
	query := tx.Model(&AgentFundSlice{}).Where("agent_user_id = ? AND state = ?", owner, AgentSliceAvailable)
	if batchID > 0 {
		query = query.Where("batch_id = ?", batchID)
	}
	if len(kinds) > 0 {
		query = query.Where("batch_id IN (?)", tx.Model(&AgentFundBatch{}).Select("id").Where("source_kind IN ?", kinds))
	}
	var available []AgentFundSlice
	if err := lockForUpdate(query).Order("batch_id ASC, start_quota ASC").Find(&available).Error; err != nil {
		return nil, err
	}
	remaining := quota
	result := make([]AgentFundSlice, 0)
	for _, original := range available {
		if remaining == 0 {
			break
		}
		if original.Quota <= 0 {
			return nil, ErrAgentFundInvariant
		}
		take := min(original.Quota, remaining)
		var batch AgentFundBatch
		if err := tx.First(&batch, original.BatchID).Error; err != nil {
			return nil, err
		}
		economics, err := ensureAgentFundEconomicsTx(tx, &batch)
		if err != nil {
			return nil, err
		}
		if economics != nil && economics.Scheme != AgentFundEconomicCumulative {
			if batchID == 0 {
				continue
			}
			return nil, ErrAgentFundEconomicReview
		}
		if batch.SourceKind == AgentFundCredit && (state == AgentSliceReserved || state == AgentSliceSpent) {
			if err := agentCreditSourceSelectableTx(tx, &batch, state); err != nil {
				if batchID == 0 && (errors.Is(err, ErrAgentCreditRisk) || errors.Is(err, ErrAgentFeatureDisabled)) {
					continue
				}
				return nil, err
			}
		}
		changed := original
		changed.Quota = take
		changed.State = state
		changed.RedemptionID = redemptionID
		changed.RefundID = refundID
		if len(reclassificationID) > 0 {
			changed.ReclassificationID = reclassificationID[0]
		}
		update := tx.Model(&AgentFundSlice{}).Where("id = ? AND state = ? AND quota = ?", original.ID, AgentSliceAvailable, original.Quota).Updates(map[string]any{"quota": take, "state": state, "redemption_id": redemptionID, "refund_id": refundID, "reclassification_id": changed.ReclassificationID})
		if update.Error != nil {
			return nil, update.Error
		}
		if update.RowsAffected != 1 {
			return nil, ErrAgentFundInvariant
		}
		if original.Quota > take {
			tail := AgentFundSlice{BatchID: original.BatchID, AgentUserID: owner, StartQuota: original.StartQuota + take, Quota: original.Quota - take, State: AgentSliceAvailable}
			if err := tx.Create(&tail).Error; err != nil {
				return nil, err
			}
		}
		if _, err := appendAgentFundLedgerTx(tx, &batch, &changed, AgentSliceAvailable, operation); err != nil {
			return nil, err
		}
		result = append(result, changed)
		remaining -= take
	}
	if remaining != 0 {
		return nil, ErrAgentQuotaInsufficient
	}
	return result, nil
}

func transitionAgentFundSlicesTx(tx *gorm.DB, owner, redemptionID, refundID int, from, to, operation string, reclassificationID ...int) (int, int64, error) {
	query := tx.Where("agent_user_id = ? AND state = ?", owner, from)
	if redemptionID > 0 {
		query = query.Where("redemption_id = ?", redemptionID)
	} else if refundID > 0 {
		query = query.Where("refund_id = ?", refundID)
	} else if len(reclassificationID) > 0 && reclassificationID[0] > 0 {
		query = query.Where("reclassification_id = ?", reclassificationID[0])
	} else {
		return 0, 0, ErrAgentQuotaInvalid
	}
	var slices []AgentFundSlice
	if err := lockForUpdate(query).Order("batch_id ASC, start_quota ASC").Find(&slices).Error; err != nil {
		return 0, 0, err
	}
	quota := 0
	cost := int64(0)
	for i := range slices {
		slice := &slices[i]
		var batch AgentFundBatch
		if err := tx.First(&batch, slice.BatchID).Error; err != nil {
			return 0, 0, err
		}
		if _, err := ensureAgentFundEconomicsTx(tx, &batch); err != nil {
			return 0, 0, err
		}
		slice.State = to
		// Book provenance and actual economics have separate immutable journals.
		value, err := appendAgentFundLedgerTx(tx, &batch, slice, from, operation)
		if err != nil {
			return 0, 0, err
		}
		if slice.Quota > common.MaxWalletQuota-quota || value < 0 || value > int64(^uint64(0)>>1)-cost {
			return 0, 0, ErrAgentFundInvariant
		}
		quota += slice.Quota
		cost += value
		updates := map[string]any{"state": to}
		if to == AgentSliceAvailable {
			updates["redemption_id"] = 0
			updates["refund_id"] = 0
			updates["reclassification_id"] = 0
		}
		if err := tx.Model(&AgentFundSlice{}).Where("id = ?", slice.ID).Updates(updates).Error; err != nil {
			return 0, 0, err
		}
	}
	return quota, cost, nil
}

// CreditAgentCommissionBatchTx never applies procurement pricing and never
// creates cash-refundable principal. The enclosing conversion owns authorization.
func CreditAgentCommissionBatchTx(tx *gorm.DB, ownerID int, operationID, currency string, amountMinor int64, quota int, policyVersion int64) error {
	if amountMinor <= 0 || amountMinor > int64(common.MaxWalletQuota) || quota <= 0 || quota > common.MaxWalletQuota || validateAgentRequestID(operationID) != nil {
		return ErrAgentQuotaInvalid
	}
	key := "commission:" + operationID
	var existing AgentFundBatch
	err := tx.Where("source_key = ?", key).First(&existing).Error
	if err == nil {
		if existing.AgentUserID != ownerID || existing.OriginalQuota != quota || existing.Currency != currency || existing.OriginalPaidMinor != strconv.FormatInt(amountMinor, 10) || existing.PolicyVersion != policyVersion {
			return ErrAgentQuotaConflict
		}
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	account, err := lockAgentRedemptionAccount(tx, ownerID, true)
	if err != nil {
		return err
	}
	if err := ensureAgentFundSourcesTx(tx, account); err != nil {
		return err
	}
	var originalPolicy AgentPolicy
	if err := tx.First(&originalPolicy, policyVersion).Error; err != nil {
		return err
	}
	if originalPolicy.CommissionCurrency != currency || !validAgentCurrency(currency, originalPolicy.CommissionCurrencyExponent) {
		return ErrAgentQuotaConflict
	}
	snapshot, err := common.Marshal(originalPolicy)
	if err != nil {
		return err
	}
	batch := AgentFundBatch{SourceKey: key, AgentUserID: ownerID, SourceKind: AgentFundCommission, CurrencyExponent: originalPolicy.CommissionCurrencyExponent, OriginalQuota: quota, OriginalPaidMinor: strconv.FormatInt(amountMinor, 10), Currency: currency, WalletAccount: AccountReward, PolicyVersion: policyVersion, PolicySnapshot: string(snapshot)}
	if err := createAgentFundBatchTx(tx, &batch); err != nil {
		return err
	}
	return applyAgentQuotaChangeTx(tx, account, AgentRedemptionLedger{OperationKey: key, ActorUserID: ownerID, Kind: "commission_transfer", Delta: quota, AllocatedDelta: quota, RequestID: operationID, Reason: "Commission transferred to non-refundable agent quota"})
}

func MigrateLegacyAgentFunds() error { return MigrateLegacyAgentFundsWithDB(DB) }

func MigrateLegacyAgentFundsWithDB(db *gorm.DB) error {
	var ids []int
	if err := db.Model(&AgentRedemptionAccount{}).Order("agent_user_id ASC").Pluck("agent_user_id", &ids).Error; err != nil {
		return err
	}
	for _, id := range ids {
		err := db.Transaction(func(tx *gorm.DB) error {
			if _, err := LockCurrentAgentPolicyTx(tx); err != nil {
				return err
			}
			account, err := lockAgentRedemptionAccount(tx, id, false)
			if err != nil {
				return err
			}
			return ensureAgentFundSourcesTx(tx, account)
		})
		if errors.Is(err, ErrAgentFundInvariant) || errors.Is(err, ErrAgentQuotaInvariant) {
			if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&AgentFundAnomaly{AgentUserID: id, Code: "legacy_source_mismatch", Detail: "Legacy balances or outstanding code reservations require manual reconciliation; no funds changed"}).Error; err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func reserveRedemptionFundSourcesTx(tx *gorm.DB, redemption *Redemption, operation string) error {
	slices, err := takeAgentFundSlicesTx(tx, redemption.BudgetAgentID, redemption.Quota, 0, nil, AgentSliceReserved, operation, redemption.Id, 0)
	if err != nil {
		return err
	}
	for _, slice := range slices {
		var batch AgentFundBatch
		if err := tx.First(&batch, slice.BatchID).Error; err != nil {
			return err
		}
		if batch.SourceKind == AgentFundPurchase || batch.SourceKind == AgentFundCommission || batch.SourceKind == AgentFundCredit {
			redemption.SourceProtected = true
		}
	}
	return tx.Model(redemption).Update("source_protected", redemption.SourceProtected).Error
}

func creditAgentRedemptionWalletTx(tx *gorm.DB, redemption *Redemption, userID int) (*LedgerResult, error) {
	entry := LedgerEntry{UserId: userID, Account: AccountPaid, Delta: redemption.Quota, Reason: LedgerReasonRedemption, ReferenceType: "redemption", ReferenceId: strconv.Itoa(redemption.Id)}
	if redemption.BudgetAgentID == 0 {
		return CreditQuotaTx(tx, entry)
	}
	var slices []AgentFundSlice
	if err := tx.Where("agent_user_id = ? AND redemption_id = ? AND state = ?", redemption.BudgetAgentID, redemption.Id, AgentSliceSpent).Find(&slices).Error; err != nil {
		return nil, err
	}
	amounts := map[LedgerAccount]int{}
	total := 0
	for _, slice := range slices {
		var batch AgentFundBatch
		if err := tx.First(&batch, slice.BatchID).Error; err != nil {
			return nil, err
		}
		if slice.Quota <= 0 || slice.Quota > redemption.Quota-total {
			return nil, ErrAgentFundInvariant
		}
		total += slice.Quota
		if _, valid := ledgerAccountColumn[batch.WalletAccount]; !valid {
			return nil, ErrAgentFundInvariant
		}
		amounts[batch.WalletAccount] += slice.Quota
	}
	if total != redemption.Quota {
		return nil, ErrAgentFundInvariant
	}
	shares := make([]LedgerShare, 0, 3)
	for _, account := range []LedgerAccount{AccountPaid, AccountBonus, AccountReward} {
		if amounts[account] > 0 {
			shares = append(shares, LedgerShare{Account: account, Amount: amounts[account]})
		}
	}
	return CreditQuotaSharesTx(tx, entry, shares)
}

// Fee intervals use the original cumulative paid-minor boundaries. Splitting
// the same refundable source cannot repeatedly discard a fractional fee.
func AgentFundSliceRefundFee(batch *AgentFundBatch, start, quota int, feeBPS int64) (int64, error) {
	if feeBPS < 0 || feeBPS > 10000 {
		return 0, ErrAgentPolicyInvalid
	}
	cost, err := AgentFundSliceCost(batch, start, quota)
	if err != nil {
		return 0, err
	}
	before := int64(0)
	if start > 0 {
		before, err = AgentFundSliceCost(batch, 0, start)
		if err != nil {
			return 0, err
		}
	}
	first, err := AgentMoneyRatio(before, feeBPS, 10000, false)
	if err != nil {
		return 0, err
	}
	last, err := AgentMoneyRatio(before+cost, feeBPS, 10000, false)
	if err != nil {
		return 0, err
	}
	return last - first, nil
}
