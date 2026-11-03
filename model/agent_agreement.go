package model

import (
	"errors"
	"math/big"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var (
	ErrAgentAgreementInvalid  = errors.New("Invalid agent commercial agreement")
	ErrAgentAgreementConflict = errors.New("Agent agreement conflicts with approved terms or changed state")
	ErrAgentAgreementReview   = errors.New("Agent agreement requires an independent finance reviewer")
)

// Thresholds are half-open. base_paid_minor is the actual payable amount at the
// explicitly configured BASE rate, BEFORE selecting a tier; never the tier's
// own resulting price. A tier replaces the base rate, it never compounds it.
type AgentAgreementTier struct {
	Key     string `json:"key"`
	Basis   string `json:"basis"`
	Minimum int64  `json:"minimum,string"`
	Maximum int64  `json:"maximum,string"`
	RateBPS int64  `json:"rate_bps"`
}
type AgentAgreementTerms struct {
	AgreementKey     string               `json:"agreement_key"`
	TermsVersion     int64                `json:"terms_version"`
	Scope            string               `json:"scope"`
	AgentUserID      int                  `json:"agent_user_id"`
	Currency         string               `json:"currency"`
	CurrencyExponent int                  `json:"currency_exponent"`
	QuotaNumerator   int64                `json:"quota_numerator,string"`
	QuotaDenominator int64                `json:"quota_denominator,string"`
	BaseRateBPS      int64                `json:"base_rate_bps"`
	EffectiveFrom    int64                `json:"effective_from"`
	EffectiveUntil   int64                `json:"effective_until"`
	DossierReference string               `json:"dossier_reference"`
	Tiers            []AgentAgreementTier `json:"tiers"`
}

// A new revision is a new row. Terms and their digest are never overwritten.
type AgentAgreement struct {
	ID             int64  `json:"id" gorm:"primaryKey"`
	AgreementKey   string `json:"agreement_key" gorm:"type:varchar(96);not null;uniqueIndex:idx_agent_agreement_revision,priority:1"`
	TermsVersion   int64  `json:"terms_version" gorm:"not null;uniqueIndex:idx_agent_agreement_revision,priority:2"`
	Scope          string `json:"scope" gorm:"type:varchar(16);not null;index"`
	AgentUserID    int    `json:"agent_user_id" gorm:"not null;index"`
	PolicyVersion  int64  `json:"policy_version" gorm:"not null"`
	ProfileVersion int64  `json:"profile_version" gorm:"not null"`
	EffectiveFrom  int64  `json:"effective_from" gorm:"not null;index"`
	EffectiveUntil int64  `json:"effective_until" gorm:"not null"`
	Currency       string `json:"currency" gorm:"type:varchar(3);not null"`
	TermsJSON      string `json:"-" gorm:"type:text;not null"`
	TermsDigest    string `json:"terms_digest" gorm:"type:char(64);not null"`
	CreatedBy      int    `json:"created_by" gorm:"not null"`
	CreatedAt      int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentAgreement) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentAgreement) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

type AgentAgreementApproval struct {
	AgreementID int64  `json:"agreement_id" gorm:"primaryKey;autoIncrement:false"`
	State       string `json:"state" gorm:"type:varchar(16);not null;index"`
	Version     int64  `json:"version" gorm:"not null"`
	ApprovedBy  int    `json:"approved_by"`
	UpdatedAt   int64  `json:"updated_at"`
}
type AgentAgreementAudit struct {
	ID          int64  `json:"id" gorm:"primaryKey"`
	AgreementID int64  `json:"agreement_id" gorm:"not null;index"`
	Action      string `json:"action" gorm:"type:varchar(32);not null"`
	ActorUserID int    `json:"actor_user_id" gorm:"not null"`
	Reason      string `json:"reason" gorm:"type:varchar(255);not null"`
	RequestID   string `json:"request_id" gorm:"type:varchar(96);not null;uniqueIndex"`
	Signature   string `json:"-" gorm:"type:char(64);not null"`
	CreatedAt   int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentAgreementAudit) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentAgreementAudit) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }
func AgentAgreementModels() []any {
	return []any{&AgentAgreement{}, &AgentAgreementApproval{}, &AgentAgreementAudit{}}
}

type AgentAgreementView struct {
	AgentAgreement
	Terms    AgentAgreementTerms    `json:"terms"`
	Approval AgentAgreementApproval `json:"approval"`
}
type AgentAgreementCommand struct {
	RequestID       string              `json:"request_id"`
	AgreementID     int64               `json:"agreement_id"`
	ExpectedVersion int64               `json:"expected_version"`
	Reason          string              `json:"reason"`
	Terms           AgentAgreementTerms `json:"terms"`
}
type AgentPriceSnapshot struct {
	AgreementID           int64  `json:"agreement_id"`
	AgreementVersion      int64  `json:"agreement_version"`
	AgreementStateVersion int64  `json:"agreement_state_version"`
	TierKey               string `json:"tier_key"`
	TierVersion           int64  `json:"tier_version"`
	PolicyVersion         int64  `json:"policy_version"`
	ProfileVersion        int64  `json:"profile_version"`
	Currency              string `json:"currency"`
	CurrencyExponent      int    `json:"currency_exponent"`
	Quota                 int    `json:"quota"`
	QuotaNumerator        int64  `json:"quota_numerator,string"`
	QuotaDenominator      int64  `json:"quota_denominator,string"`
	RateBPS               int64  `json:"rate_bps"`
	FaceMinor             string `json:"face_minor"`
	BasePaidMinor         string `json:"base_paid_minor"`
	PaidMinor             string `json:"paid_minor"`
	HitReason             string `json:"hit_reason"`
}

func validAgentDossierReference(value string) bool {
	if len(value) < 3 || len(value) > 128 || strings.TrimSpace(value) != value {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_.:/-", c)) {
			return false
		}
	}
	return true
}
func ValidateAgentAgreementTerms(t AgentAgreementTerms) error {
	if validateAgentRequestID(t.AgreementKey) != nil || t.TermsVersion <= 0 || !validAgentCurrency(t.Currency, t.CurrencyExponent) || !validAgentDossierReference(t.DossierReference) || t.EffectiveFrom <= 0 || t.EffectiveUntil <= t.EffectiveFrom || t.EffectiveUntil-t.EffectiveFrom > 10*366*86400 || t.QuotaNumerator <= 0 || t.QuotaNumerator > int64(common.MaxWalletQuota) || t.QuotaDenominator <= 0 || t.QuotaDenominator > int64(common.MaxWalletQuota) || t.BaseRateBPS <= 0 || t.BaseRateBPS > 10000 || len(t.Tiers) > 50 {
		return ErrAgentAgreementInvalid
	}
	if (t.Scope != "agreement" && t.Scope != "tier") || (t.Scope == "agreement" && t.AgentUserID <= 0) || (t.Scope == "tier" && t.AgentUserID != 0) {
		return ErrAgentAgreementInvalid
	}
	keys := map[string]bool{}
	basis := ""
	for i, tier := range t.Tiers {
		if !validAgentDossierReference(tier.Key) || keys[tier.Key] || tier.Minimum < 0 || tier.Maximum <= tier.Minimum || tier.Maximum > int64(common.MaxWalletQuota)+1 || tier.RateBPS <= 0 || tier.RateBPS > 10000 {
			return ErrAgentAgreementInvalid
		}
		if tier.Basis != "quota" && tier.Basis != "face_minor" && tier.Basis != "base_paid_minor" {
			return ErrAgentAgreementInvalid
		}
		// Mixing threshold units is deliberately refused rather than guessed/sorted.
		if basis != "" && basis != tier.Basis {
			return ErrAgentAgreementInvalid
		}
		basis = tier.Basis
		keys[tier.Key] = true
		for _, prior := range t.Tiers[:i] {
			if tier.Minimum < prior.Maximum && prior.Minimum < tier.Maximum {
				return ErrAgentAgreementConflict
			}
		}
	}
	return nil
}

// Agreement pricing may replace the approved rate, not silently redefine the
// currency/quota unit and thereby bypass the root-approved rate floor.
func validateAgentAgreementPolicy(t AgentAgreementTerms, p *AgentPolicy) error {
	if err := ValidateAgentAgreementTerms(t); err != nil {
		return err
	}
	if p.AgreementMinimumRateBPS <= 0 || t.BaseRateBPS < p.AgreementMinimumRateBPS || t.Currency != p.PurchaseCurrency || t.CurrencyExponent != p.PurchaseCurrencyExponent {
		return ErrAgentAgreementInvalid
	}
	left := new(big.Int).Mul(big.NewInt(t.QuotaNumerator), big.NewInt(p.PurchaseQuotaDenominator))
	right := new(big.Int).Mul(big.NewInt(p.PurchaseQuotaNumerator), big.NewInt(t.QuotaDenominator))
	if left.Cmp(right) != 0 {
		return ErrAgentAgreementInvalid
	}
	for _, tier := range t.Tiers {
		if tier.RateBPS < p.AgreementMinimumRateBPS {
			return ErrAgentAgreementInvalid
		}
	}
	return nil
}

func agentPriceMinor(quota int, numerator, denominator, rate int64) (int64, error) {
	if quota <= 0 || quota > common.MaxWalletQuota || numerator <= 0 || denominator <= 0 || rate <= 0 || rate > 10000 {
		return 0, ErrAgentAgreementInvalid
	}
	n := new(big.Int).Mul(big.NewInt(int64(quota)), big.NewInt(denominator))
	n.Mul(n, big.NewInt(rate))
	d := new(big.Int).Mul(big.NewInt(numerator), big.NewInt(10000))
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(n, d, r)
	if r.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() || q.Sign() <= 0 || q.Int64() > int64(common.MaxWalletQuota) {
		return 0, ErrWalletQuotaLimitExceeded
	}
	return q.Int64(), nil
}

// ResolveAgentPurchasePriceTx prices ONLY new cash procurement. Commission
// conversions and credit lending must use their own explicit product policy.
// Writers hold the current policy lock first. All rate math is single-round,
// exact rational arithmetic; old orders never call this resolver again.
func ResolveAgentPurchasePriceTx(tx *gorm.DB, p *AgentPolicy, owner, quota int, at int64) (AgentPriceSnapshot, error) {
	s := AgentPriceSnapshot{PolicyVersion: p.ID, Currency: p.PurchaseCurrency, CurrencyExponent: p.PurchaseCurrencyExponent, Quota: quota, QuotaNumerator: p.PurchaseQuotaNumerator, QuotaDenominator: p.PurchaseQuotaDenominator, RateBPS: p.PurchaseRateBPS, HitReason: "global_policy"}
	if _, err := agentPurchaseQuotePrice(p, quota); err != nil {
		return s, err
	}
	profile, err := readAgentProfile(tx, owner, false)
	if err != nil {
		return s, err
	}
	if profile != nil {
		s.ProfileVersion = profile.Version
	}
	base := p.PurchaseRateBPS
	var selected *AgentAgreement
	var approval AgentAgreementApproval
	if p.AgreementPricingEnabled {
		var rows []AgentAgreement
		if err := tx.Model(&AgentAgreement{}).Joins("JOIN agent_agreement_approvals a ON a.agreement_id = agent_agreements.id").Where("a.state = ? AND agent_agreements.policy_version = ? AND agent_agreements.currency = ? AND agent_agreements.effective_from <= ? AND agent_agreements.effective_until > ? AND agent_agreements.agent_user_id IN ?", "approved", p.ID, p.PurchaseCurrency, at, at, []int{0, owner}).Find(&rows).Error; err != nil {
			return s, err
		}
		var personal, global []*AgentAgreement
		for i := range rows {
			row := &rows[i]
			if row.Scope == "agreement" && row.AgentUserID == owner && row.ProfileVersion == s.ProfileVersion {
				personal = append(personal, row)
			} else if row.Scope == "tier" && row.AgentUserID == 0 {
				global = append(global, row)
			}
		}
		if len(personal) > 1 || len(global) > 1 {
			return s, ErrAgentAgreementConflict
		}
		if len(personal) == 1 {
			selected = personal[0]
			s.HitReason = "personal_agreement"
		} else if len(global) == 1 {
			selected = global[0]
			s.HitReason = "global_tier_base"
		}
	}
	var terms AgentAgreementTerms
	if selected != nil {
		if err := common.UnmarshalJsonStr(selected.TermsJSON, &terms); err != nil {
			return s, err
		}
		if err := validateAgentAgreementPolicy(terms, p); err != nil {
			return s, err
		}
		if err := tx.First(&approval, "agreement_id = ?", selected.ID).Error; err != nil {
			return s, err
		}
		s.AgreementID = selected.ID
		s.AgreementVersion = selected.TermsVersion
		s.AgreementStateVersion = approval.Version
		s.QuotaNumerator = terms.QuotaNumerator
		s.QuotaDenominator = terms.QuotaDenominator
		s.RateBPS = terms.BaseRateBPS
		base = terms.BaseRateBPS
	}
	face, err := agentPriceMinor(quota, s.QuotaNumerator, s.QuotaDenominator, 10000)
	if err != nil {
		return s, err
	}
	basePaid, err := agentPriceMinor(quota, s.QuotaNumerator, s.QuotaDenominator, base)
	if err != nil {
		return s, err
	}
	s.FaceMinor = strconv.FormatInt(face, 10)
	s.BasePaidMinor = strconv.FormatInt(basePaid, 10)
	for _, tier := range terms.Tiers {
		value := int64(quota)
		if tier.Basis == "face_minor" {
			value = face
		} else if tier.Basis == "base_paid_minor" {
			value = basePaid
		}
		if value >= tier.Minimum && value < tier.Maximum {
			s.RateBPS = tier.RateBPS
			s.TierKey = tier.Key
			s.TierVersion = terms.TermsVersion
			if terms.Scope == "agreement" {
				s.HitReason = "personal_agreement_tier"
			} else {
				s.HitReason = "global_tier"
			}
		}
	}
	paid, err := agentPriceMinor(quota, s.QuotaNumerator, s.QuotaDenominator, s.RateBPS)
	if err != nil {
		return s, err
	}
	s.PaidMinor = strconv.FormatInt(paid, 10)
	return s, nil
}
