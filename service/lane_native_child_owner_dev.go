//go:build lane_billing_integration_dev

package service

import (
	"context"

	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// BindLaneNativeReleaseDev consumes the actual immutable prehold/final wire,
// not a request-local digest copied into a fabricated child reservation. It
// cannot install a store or select a route. The original Release quote and
// both custody slots are revalidated in the canonical model transaction.
func (w LaneNativeReleaseWireV1) BindLaneNativeReleaseDev(ctx context.Context, db *gorm.DB, b model.LaneBillingChildDevBinding) (*model.LaneBillingChildDev, LaneNativeChatProof, error) {
	e := w.proof.Evidence()
	if w.proof.BodySize() == 0 || b.Owner.RequestID != "" || b.ChannelID != e.ChannelID ||
		b.FrozenPriceSHA256 != "" || b.RouteSHA256 != "" || b.FinalWireSHA256 != "" {
		return nil, LaneNativeChatProof{}, model.ErrLaneBillingOwnerEvidence
	}
	b.Owner.RequestID = e.RequestID
	b.FrozenPriceSHA256, b.RouteSHA256, b.FinalWireSHA256 = e.FrozenPriceSHA256, e.RouteSHA256, e.FinalWireSHA256
	child, err := model.ReserveLaneNativeChildDev(ctx, db, w.draft.facts, b, e.QuotaCap)
	if err != nil {
		return nil, LaneNativeChatProof{}, err
	}
	return child, w.proof, nil
}
