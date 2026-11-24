package service

import (
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// LaneStoreBindingDecisionV1 separates default policy from financial ability.
// A verified eligible shape on this release still has CanBind false.
type LaneStoreBindingDecisionV1 struct {
	DefaultEligible bool
	CanBind         bool
}

// LaneStoreBindingDraftV1 ties the actual sealed Native Chat prepared price to
// the opaque PostgreSQL quote facts. No normal request constructs it today.
type LaneStoreBindingDraftV1 struct {
	facts model.LaneStoreQuoteAuthorityV1
	price LaneNativeChatPreparedPrice
}

func PrepareLaneStoreBindingDraftV1(price LaneNativeChatPreparedPrice, facts model.LaneStoreQuoteAuthorityV1) (LaneStoreBindingDraftV1, error) {
	q := price.quote
	if !laneNativeRequestID(q.RequestID) || !laneNativeName(q.Group) || q.UserRevision != 0 ||
		!facts.MatchesPreparedRequest(q.RequestID, q.Group, q.GroupConfigVersion) {
		return LaneStoreBindingDraftV1{}, model.ErrLaneStoreAuthorityEvidence
	}
	return LaneStoreBindingDraftV1{facts: facts, price: price}, nil
}

// VerifyLaneStoreFinalBindingDraftV1Tx never reserves or grants dispatch. The
// Lab's public Evidence value is not accepted: the complete sealed local
// proof and quote must match, then the caller's exact bounded transaction
// revalidates the current release, paid backing and original finite Key.
func VerifyLaneStoreFinalBindingDraftV1Tx(tx *gorm.DB, d LaneStoreBindingDraftV1, p LaneNativeChatProof) (LaneStoreBindingDecisionV1, error) {
	q, e := d.price.quote, p.evidence
	if p.quote != q || len(p.body) == 0 || !d.facts.MatchesPreparedRequest(q.RequestID, q.Group, q.GroupConfigVersion) ||
		e.RequestID != q.RequestID || e.Group != q.Group || e.Model != q.Model || e.ChannelID <= 0 ||
		!laneNativeHash(e.FrozenPriceSHA256) || !laneNativeHash(e.RouteSHA256) || !laneNativeHash(e.FinalWireSHA256) {
		return LaneStoreBindingDecisionV1{}, model.ErrLaneStoreAuthorityEvidence
	}
	if err := model.RevalidateLaneStoreQuoteAuthorityV1Tx(tx, d.facts, e.QuotaCap); err != nil {
		return LaneStoreBindingDecisionV1{}, err
	}
	// Release/route/plans can prove eligibility, not make this binary
	// capable. No authorization relies on a mutable registry or DB flag.
	return LaneStoreBindingDecisionV1{DefaultEligible: true, CanBind: model.LaneStoreRuntimeCapable}, nil
}
