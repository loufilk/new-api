package model

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const AgentFundEconomicCumulative = "cumulative_v1"
const AgentFundEconomicLegacyException = "legacy_exception"

var ErrAgentFundEconomicReview = errors.New("Funding valuation requires independent review; existing customer rights are preserved")

// This is derived source valuation carry, NEVER a wallet or extra credit limit.
// For a clean batch: C=ceil(P*S/Q), L=floor(P*R/Q), reserve=P-C-L.
// S is irrevocably spent quota; R is successfully refunded/released quota.
// Interval book cost remains immutable provenance, not cash/AR authority.
type AgentFundEconomics struct {
	BatchID        int    `json:"batch_id" gorm:"primaryKey;autoIncrement:false"`
	AgentUserID    int    `json:"agent_user_id" gorm:"index;not null"`
	SourceKind     string `json:"source_kind" gorm:"type:varchar(32);not null"`
	OriginalQuota  int    `json:"original_quota" gorm:"type:bigint;not null"`
	PrincipalMinor int64  `json:"principal_minor,string"`
	SpentQuota     int    `json:"spent_quota" gorm:"type:bigint"`
	ReleasedQuota  int    `json:"released_quota" gorm:"type:bigint"`
	ChargedMinor   int64  `json:"charged_minor,string"`
	ReleasedMinor  int64  `json:"released_minor,string"`
	Scheme         string `json:"scheme" gorm:"type:varchar(32);not null"`
	Version        int64  `json:"version"`
	UpdatedAt      int64  `json:"updated_at" gorm:"autoUpdateTime"`
}

func (*AgentFundEconomics) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

type AgentFundEconomicLedger struct {
	ID                       int64  `json:"id" gorm:"primaryKey"`
	EventKey                 string `json:"event_key" gorm:"type:varchar(240);not null;uniqueIndex"`
	BatchID                  int    `json:"batch_id" gorm:"index;not null"`
	AgentUserID              int    `json:"agent_user_id" gorm:"index;not null"`
	Kind                     string `json:"kind" gorm:"type:varchar(32);not null"`
	Scheme                   string `json:"scheme" gorm:"type:varchar(32);not null"`
	SpentDelta               int    `json:"spent_delta" gorm:"type:bigint"`
	ReleasedDelta            int    `json:"released_delta" gorm:"type:bigint"`
	ChargedDeltaMinor        int64  `json:"charged_delta_minor,string"`
	ReleasedDeltaMinor       int64  `json:"released_delta_minor,string"`
	TargetChargedDeltaMinor  int64  `json:"target_charged_delta_minor,string"`
	TargetReleasedDeltaMinor int64  `json:"target_released_delta_minor,string"`
	SpentAfter               int    `json:"spent_after" gorm:"type:bigint"`
	ReleasedAfter            int    `json:"released_after" gorm:"type:bigint"`
	ChargedAfterMinor        int64  `json:"charged_after_minor,string"`
	ReleasedAfterMinor       int64  `json:"released_after_minor,string"`
	CreatedAt                int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentFundEconomicLedger) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentFundEconomicLedger) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

type AgentFundEconomicAnomaly struct {
	ID          int64  `json:"id" gorm:"primaryKey"`
	EventKey    string `json:"event_key" gorm:"type:varchar(240);not null;uniqueIndex"`
	BatchID     int    `json:"batch_id" gorm:"index;not null"`
	AgentUserID int    `json:"agent_user_id" gorm:"index;not null"`
	Code        string `json:"code" gorm:"type:varchar(64);not null"`
	Snapshot    string `json:"snapshot" gorm:"type:text;not null"`
	CreatedAt   int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentFundEconomicAnomaly) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentFundEconomicAnomaly) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }
func AgentFundEconomicsModels() []any {
	return []any{&AgentFundEconomics{}, &AgentFundEconomicLedger{}, &AgentFundEconomicAnomaly{}, &AgentFundLegacyRefundAuthority{}}
}

type AgentFundEconomicPreview struct {
	Version             int64 `json:"version"`
	SpentQuota          int   `json:"spent_quota"`
	ReleasedQuota       int   `json:"released_quota"`
	ChargedMinor        int64 `json:"charged_minor,string"`
	ReleasedMinor       int64 `json:"released_minor,string"`
	ChargedDeltaMinor   int64 `json:"charged_delta_minor,string"`
	ReleasedDeltaMinor  int64 `json:"released_delta_minor,string"`
	ReleasedBeforeMinor int64 `json:"released_before_minor,string"`
	ReleasedAfterMinor  int64 `json:"released_after_minor,string"`
}

func agentFundEconomicTargets(p int64, q, s, r int) (int64, int64, error) {
	if p < 0 || p > int64(common.MaxWalletQuota) || q <= 0 || q > common.MaxWalletQuota || s < 0 || r < 0 || s > q || r > q-s {
		return 0, 0, ErrAgentFundInvariant
	}
	charged, err := AgentMoneyRatio(int64(s), p, int64(q), true)
	if err != nil {
		return 0, 0, err
	}
	released, err := AgentMoneyRatio(int64(r), p, int64(q), false)
	if err != nil {
		return 0, 0, err
	}
	if charged > p || released > p-charged {
		return 0, 0, ErrAgentFundInvariant
	}
	return charged, released, nil
}
func validateAgentFundEconomics(e *AgentFundEconomics) error {
	charged, released, err := agentFundEconomicTargets(e.PrincipalMinor, e.OriginalQuota, e.SpentQuota, e.ReleasedQuota)
	if err != nil {
		return err
	}
	if e.Version <= 0 || e.ChargedMinor < 0 || e.ReleasedMinor < 0 || e.ChargedMinor > e.PrincipalMinor || e.ReleasedMinor > e.PrincipalMinor-e.ChargedMinor {
		return ErrAgentFundInvariant
	}
	if e.Scheme == AgentFundEconomicCumulative {
		if charged != e.ChargedMinor || released != e.ReleasedMinor {
			return ErrAgentFundInvariant
		}
	} else if e.Scheme != AgentFundEconomicLegacyException {
		return ErrAgentFundInvariant
	}
	return nil
}
func agentFundHasEconomics(b *AgentFundBatch) bool {
	return b.SourceKind == AgentFundPurchase || b.SourceKind == AgentFundCredit
}

// Initialization observes immutable historical facts. It does not reprice past
// payments, recognize additional debt, or change any wallet/account/source row.
func ensureAgentFundEconomicsTx(tx *gorm.DB, b *AgentFundBatch) (*AgentFundEconomics, error) {
	if !agentFundHasEconomics(b) {
		return nil, nil
	}
	var e AgentFundEconomics
	err := lockForUpdate(tx).Where("batch_id = ?", b.ID).First(&e).Error
	if err == nil {
		if e.AgentUserID != b.AgentUserID || e.SourceKind != b.SourceKind || e.OriginalQuota != b.OriginalQuota || strconv.FormatInt(e.PrincipalMinor, 10) != b.OriginalPaidMinor {
			return nil, ErrAgentFundInvariant
		}
		return &e, validateAgentFundEconomics(&e)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	p, err := agentCreditMinor(b.OriginalPaidMinor, true)
	if err != nil {
		return nil, ErrAgentFundInvariant
	}
	e = AgentFundEconomics{BatchID: b.ID, AgentUserID: b.AgentUserID, SourceKind: b.SourceKind, OriginalQuota: b.OriginalQuota, PrincipalMinor: p, Scheme: AgentFundEconomicCumulative, Version: 1}
	var slices []AgentFundSlice
	if err := tx.Where("batch_id = ?", b.ID).Order("start_quota").Find(&slices).Error; err != nil {
		return nil, err
	}
	start := 0
	for _, s := range slices {
		if s.AgentUserID != b.AgentUserID || s.StartQuota != start || s.Quota <= 0 || s.Quota > b.OriginalQuota-start {
			return nil, ErrAgentFundInvariant
		}
		start += s.Quota
		switch s.State {
		case AgentSliceSpent:
			e.SpentQuota += s.Quota
		case AgentSliceRefunded:
			if b.SourceKind != AgentFundPurchase {
				return nil, ErrAgentFundInvariant
			}
			e.ReleasedQuota += s.Quota
		case AgentSliceReclaimed:
			if b.SourceKind != AgentFundCredit {
				return nil, ErrAgentFundInvariant
			}
			e.ReleasedQuota += s.Quota
		case AgentSliceAvailable, AgentSliceReserved, AgentSliceRefundFrozen:
		default:
			return nil, ErrAgentFundInvariant
		}
	}
	if start != b.OriginalQuota {
		return nil, ErrAgentFundInvariant
	}
	var originalPolicy AgentPolicy
	var historicalFees int64
	if b.SourceKind == AgentFundPurchase {
		if common.UnmarshalJsonStr(b.PolicySnapshot, &originalPolicy) != nil || originalPolicy.RefundFeeBPS < 0 || originalPolicy.RefundFeeBPS > 10000 {
			return nil, ErrAgentFundInvariant
		}
	}
	if b.SourceKind == AgentFundCredit {
		var ars []AgentReceivable
		if err := tx.Where("batch_id = ?", b.ID).Find(&ars).Error; err != nil {
			return nil, err
		}
		for _, a := range ars {
			if a.AgentUserID != b.AgentUserID || a.Currency != b.Currency || a.CurrencyExponent != b.CurrencyExponent {
				e.Scheme = AgentFundEconomicLegacyException
			}
			if a.PrincipalMinor < 0 || a.PrincipalMinor > p-e.ChargedMinor {
				return nil, ErrAgentFundInvariant
			}
			e.ChargedMinor += a.PrincipalMinor
		}
		if e.ReleasedQuota > 0 {
			var draw AgentCreditDraw
			if err := tx.Where("batch_id = ?", b.ID).First(&draw).Error; err != nil {
				return nil, err
			}
			var released []AgentCreditLedger
			if err := tx.Where("draw_id = ? AND kind = ?", draw.ID, "release").Find(&released).Error; err != nil {
				return nil, err
			}
			for _, r := range released {
				if r.AgentUserID != b.AgentUserID || r.Currency != b.Currency || r.CurrencyExponent != b.CurrencyExponent {
					e.Scheme = AgentFundEconomicLegacyException
				}
				v := -r.ReservedDelta
				if v < 0 || v > p-e.ReleasedMinor {
					return nil, ErrAgentFundInvariant
				}
				e.ReleasedMinor += v
			}
		}
	} else {
		var spent []AgentFundLedger
		if err := tx.Where("batch_id = ? AND to_state = ?", b.ID, AgentSliceSpent).Find(&spent).Error; err != nil {
			return nil, err
		}
		for _, r := range spent {
			if r.AgentUserID != b.AgentUserID || r.Currency != b.Currency || r.CurrencyExponent != b.CurrencyExponent {
				e.Scheme = AgentFundEconomicLegacyException
			}
			v, err := agentCreditMinor(r.CostMinor, false)
			if err != nil || v > p-e.ChargedMinor {
				return nil, ErrAgentFundInvariant
			}
			e.ChargedMinor += v
		}
		if b.SourceKind == AgentFundPurchase {
			var refunds []AgentPurchaseRefund
			if err := tx.Where("batch_id = ? AND state = ?", b.ID, "succeeded").Find(&refunds).Error; err != nil {
				return nil, err
			}
			releasedQuota := 0
			for _, r := range refunds {
				if r.AgentUserID != b.AgentUserID || r.Currency != b.Currency || r.CurrencyExponent != b.CurrencyExponent || r.PaymentReceiptID != b.PaymentReceiptID {
					e.Scheme = AgentFundEconomicLegacyException
				}
				v, err := agentCreditMinor(r.PrincipalMinor, true)
				if err != nil || v > p-e.ReleasedMinor || r.Quota <= 0 || r.Quota > b.OriginalQuota-releasedQuota {
					return nil, ErrAgentFundInvariant
				}
				e.ReleasedMinor += v
				fee, feeErr := agentCreditMinor(r.FeeMinor, false)
				net, netErr := agentCreditMinor(r.NetMinor, false)
				if feeErr != nil || netErr != nil || fee > v || net != v-fee || fee > p-historicalFees {
					e.Scheme = AgentFundEconomicLegacyException
				} else {
					historicalFees += fee
				}
				releasedQuota += r.Quota
			}
			if releasedQuota != e.ReleasedQuota {
				e.Scheme = AgentFundEconomicLegacyException
			}
		}
	}
	c, l, err := agentFundEconomicTargets(p, b.OriginalQuota, e.SpentQuota, e.ReleasedQuota)
	if err != nil {
		return nil, err
	}
	if e.ChargedMinor != c || e.ReleasedMinor != l {
		e.Scheme = AgentFundEconomicLegacyException
	}
	if b.SourceKind == AgentFundPurchase {
		feeBefore, err := AgentMoneyRatio(e.ReleasedMinor, originalPolicy.RefundFeeBPS, 10000, false)
		if err != nil {
			return nil, err
		}
		if historicalFees != feeBefore {
			e.Scheme = AgentFundEconomicLegacyException
		}
		var pending []AgentPurchaseRefund
		if err := tx.Where("batch_id = ? AND state IN ?", b.ID, []string{"requested", "approved", "payment_pending", "payment_unknown"}).Find(&pending).Error; err != nil {
			return nil, err
		}
		if len(pending) > 1 {
			e.Scheme = AgentFundEconomicLegacyException
		}
		for _, r := range pending {
			if err := captureAgentLegacyInflightRefundTx(tx, b, &r); err != nil {
				return nil, err
			}
			if r.AgentUserID != b.AgentUserID || r.Currency != b.Currency || r.CurrencyExponent != b.CurrencyExponent || r.PaymentReceiptID != b.PaymentReceiptID {
				e.Scheme = AgentFundEconomicLegacyException
			}
			_, target, err := agentFundEconomicTargets(p, b.OriginalQuota, e.SpentQuota, e.ReleasedQuota+r.Quota)
			amount, parseErr := agentCreditMinor(r.PrincipalMinor, true)
			fee, feeErr := agentCreditMinor(r.FeeMinor, false)
			net, netErr := agentCreditMinor(r.NetMinor, false)
			feeAfter, targetFeeErr := AgentMoneyRatio(target, originalPolicy.RefundFeeBPS, 10000, false)
			if err != nil || parseErr != nil || amount != target-l || feeErr != nil || netErr != nil || targetFeeErr != nil || fee != feeAfter-feeBefore || fee > amount || net != amount-fee {
				e.Scheme = AgentFundEconomicLegacyException
			}
		}
	}
	if err := validateAgentFundEconomics(&e); err != nil {
		return nil, err
	}
	if err := tx.Create(&e).Error; err != nil {
		return nil, err
	}
	event := "economics:init:" + strconv.Itoa(b.ID)
	entry := AgentFundEconomicLedger{EventKey: event, BatchID: b.ID, AgentUserID: b.AgentUserID, Kind: "historical_baseline", Scheme: e.Scheme, SpentDelta: e.SpentQuota, ReleasedDelta: e.ReleasedQuota, ChargedDeltaMinor: e.ChargedMinor, ReleasedDeltaMinor: e.ReleasedMinor, TargetChargedDeltaMinor: c, TargetReleasedDeltaMinor: l, SpentAfter: e.SpentQuota, ReleasedAfter: e.ReleasedQuota, ChargedAfterMinor: e.ChargedMinor, ReleasedAfterMinor: e.ReleasedMinor}
	if err := tx.Create(&entry).Error; err != nil {
		return nil, err
	}
	if e.Scheme == AgentFundEconomicLegacyException {
		snapshot, _ := common.Marshal(e)
		if err := tx.Create(&AgentFundEconomicAnomaly{EventKey: event, BatchID: b.ID, AgentUserID: b.AgentUserID, Code: "historical_valuation_incompatible", Snapshot: string(snapshot)}).Error; err != nil {
			return nil, err
		}
	}
	return &e, nil
}
func PreviewAgentFundEconomicSpendTx(tx *gorm.DB, b *AgentFundBatch, quota int) (*AgentFundEconomicPreview, error) {
	return previewAgentFundEconomicsTx(tx, b, quota, true)
}
func PreviewAgentFundEconomicReleaseTx(tx *gorm.DB, b *AgentFundBatch, quota int) (*AgentFundEconomicPreview, error) {
	return previewAgentFundEconomicsTx(tx, b, quota, false)
}
func previewAgentFundEconomicsTx(tx *gorm.DB, b *AgentFundBatch, quota int, spend bool) (*AgentFundEconomicPreview, error) {
	e, err := ensureAgentFundEconomicsTx(tx, b)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, ErrAgentFundInvariant
	}
	if e.Scheme != AgentFundEconomicCumulative {
		return nil, ErrAgentFundEconomicReview
	}
	if quota <= 0 || quota > e.OriginalQuota-e.SpentQuota-e.ReleasedQuota {
		return nil, ErrAgentQuotaInvalid
	}
	s, r := e.SpentQuota, e.ReleasedQuota
	if spend {
		s += quota
	} else {
		r += quota
	}
	c, l, err := agentFundEconomicTargets(e.PrincipalMinor, e.OriginalQuota, s, r)
	if err != nil {
		return nil, err
	}
	return &AgentFundEconomicPreview{Version: e.Version, SpentQuota: e.SpentQuota, ReleasedQuota: e.ReleasedQuota, ChargedMinor: e.ChargedMinor, ReleasedMinor: e.ReleasedMinor, ChargedDeltaMinor: c - e.ChargedMinor, ReleasedDeltaMinor: l - e.ReleasedMinor, ReleasedBeforeMinor: e.ReleasedMinor, ReleasedAfterMinor: l}, nil
}

// Called BEFORE source mutation, under policy and aggregate locks. Protected
// legacy liability uses its original interval valuation, not retroactive debt.
func applyAgentFundEconomicTransitionTx(tx *gorm.DB, b *AgentFundBatch, s *AgentFundSlice, from, operation string) (int64, error) {
	book, err := AgentFundSliceCost(b, s.StartQuota, s.Quota)
	if err != nil {
		return 0, err
	}
	if !agentFundHasEconomics(b) {
		return book, nil
	}
	e, err := ensureAgentFundEconomicsTx(tx, b)
	if err != nil {
		return 0, err
	}
	spend := s.State == AgentSliceSpent && (from == AgentSliceAvailable || from == AgentSliceReserved)
	release := (s.State == AgentSliceRefunded && from == AgentSliceRefundFrozen) || (s.State == AgentSliceReclaimed && from == AgentSliceAvailable)
	if !spend && !release {
		if e.Scheme != AgentFundEconomicCumulative && from == AgentSliceAvailable && (s.State == AgentSliceReserved || s.State == AgentSliceRefundFrozen) {
			return 0, ErrAgentFundEconomicReview
		}
		return book, nil
	}
	legacyRefund := false
	if e.Scheme != AgentFundEconomicCumulative {
		if release && from == AgentSliceRefundFrozen && s.State == AgentSliceRefunded && b.SourceKind == AgentFundPurchase {
			var refund AgentPurchaseRefund
			if err := tx.First(&refund, s.RefundID).Error; err != nil {
				return 0, err
			}
			_, intervals, err := agentLegacyRefundAuthorityTx(tx, b, &refund, false)
			if err != nil {
				return 0, err
			}
			for _, interval := range intervals {
				if interval.ID == s.ID && interval.Start == s.StartQuota && interval.Quota == s.Quota && interval.Principal == book {
					legacyRefund = true
					break
				}
			}
			if !legacyRefund {
				return 0, ErrAgentFundEconomicReview
			}
		} else if !spend || from != AgentSliceReserved {
			return 0, ErrAgentFundEconomicReview
		}
	}
	beforeC, beforeL, err := agentFundEconomicTargets(e.PrincipalMinor, e.OriginalQuota, e.SpentQuota, e.ReleasedQuota)
	if err != nil {
		return 0, err
	}
	if s.Quota <= 0 || s.Quota > e.OriginalQuota-e.SpentQuota-e.ReleasedQuota {
		return 0, ErrAgentFundInvariant
	}
	entry := AgentFundEconomicLedger{EventKey: fmt.Sprintf("%s:%d:%d", operation, b.ID, s.StartQuota), BatchID: b.ID, AgentUserID: b.AgentUserID, Scheme: e.Scheme, Kind: "spend"}
	if spend {
		e.SpentQuota += s.Quota
		entry.SpentDelta = s.Quota
	} else {
		e.ReleasedQuota += s.Quota
		entry.ReleasedDelta = s.Quota
		entry.Kind = "release"
	}
	targetC, targetL, err := agentFundEconomicTargets(e.PrincipalMinor, e.OriginalQuota, e.SpentQuota, e.ReleasedQuota)
	if err != nil {
		return 0, err
	}
	entry.TargetChargedDeltaMinor = targetC - beforeC
	entry.TargetReleasedDeltaMinor = targetL - beforeL
	delta := int64(0)
	if legacyRefund {
		if book > e.PrincipalMinor-e.ChargedMinor-e.ReleasedMinor {
			return 0, ErrAgentFundInvariant
		}
		delta = book
		e.ReleasedMinor += delta
		entry.Kind = "legacy_refund_finalize"
		entry.ReleasedDeltaMinor = delta
		snapshot, err := common.Marshal(struct {
			RefundID  int   `json:"refund_id"`
			Principal int64 `json:"principal,string"`
			Target    int64 `json:"target,string"`
		}{s.RefundID, delta, entry.TargetReleasedDeltaMinor})
		if err != nil {
			return 0, err
		}
		if err := tx.Create(&AgentFundEconomicAnomaly{EventKey: entry.EventKey, BatchID: b.ID, AgentUserID: b.AgentUserID, Code: "legacy_inflight_refund_finalized", Snapshot: string(snapshot)}).Error; err != nil {
			return 0, err
		}
	} else if e.Scheme == AgentFundEconomicLegacyException {
		delta = min(book, e.PrincipalMinor-e.ChargedMinor-e.ReleasedMinor)
		entry.Kind = "legacy_protected_spend"
		entry.ChargedDeltaMinor = delta
		e.ChargedMinor += delta
		snapshot, _ := common.Marshal(struct {
			BatchID          int   `json:"batch_id"`
			StartQuota       int   `json:"start_quota"`
			Quota            int   `json:"quota"`
			LegacyMinor      int64 `json:"legacy_minor,string"`
			TargetDeltaMinor int64 `json:"target_delta_minor,string"`
		}{b.ID, s.StartQuota, s.Quota, delta, entry.TargetChargedDeltaMinor})
		if err := tx.Create(&AgentFundEconomicAnomaly{EventKey: entry.EventKey, BatchID: b.ID, AgentUserID: b.AgentUserID, Code: "protected_legacy_valuation_preserved", Snapshot: string(snapshot)}).Error; err != nil {
			return 0, err
		}
	} else if spend {
		delta = targetC - e.ChargedMinor
		entry.ChargedDeltaMinor = delta
		e.ChargedMinor = targetC
	} else {
		delta = targetL - e.ReleasedMinor
		entry.ReleasedDeltaMinor = delta
		e.ReleasedMinor = targetL
	}
	e.Version++
	if err := validateAgentFundEconomics(e); err != nil {
		return 0, err
	}
	entry.SpentAfter = e.SpentQuota
	entry.ReleasedAfter = e.ReleasedQuota
	entry.ChargedAfterMinor = e.ChargedMinor
	entry.ReleasedAfterMinor = e.ReleasedMinor
	if err := tx.Create(&entry).Error; err != nil {
		return 0, err
	}
	if err := tx.Model(e).Select("spent_quota", "released_quota", "charged_minor", "released_minor", "version").Updates(e).Error; err != nil {
		return 0, err
	}
	return delta, nil
}
