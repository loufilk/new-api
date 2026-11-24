package service

import (
	"context"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// LaneNativeReleasePreparedV1 binds the prehold quote to a separately vetted
// exact route. It carries no financial owner, stored key, or dispatch token.
type LaneNativeReleasePreparedV1 struct {
	draft LaneStoreBindingDraftV1
	route LaneNativeChatRoute
}

// LaneNativeReleaseWireV1 bundles opaque Stage 8 release facts with the sealed
// final wire. Reconstructing it from public evidence hashes is not possible.
type LaneNativeReleaseWireV1 struct {
	draft LaneStoreBindingDraftV1
	proof LaneNativeChatProof
}

func PrepareLaneNativeReleaseV1(price LaneNativeChatPreparedPrice, facts model.LaneStoreQuoteAuthorityV1, route LaneNativeChatRoute) (LaneNativeReleasePreparedV1, error) {
	draft, err := PrepareLaneStoreBindingDraftV1(price, facts)
	if err != nil || route.ChannelID <= 0 || route.ChannelType != constant.ChannelTypeOpenAI ||
		route.Group != price.quote.Group || route.Model != price.quote.Model || route.Path != "/v1/chat/completions" ||
		!laneNativeHash(route.AuthoritySHA256) {
		return LaneNativeReleasePreparedV1{}, model.ErrLaneStoreAuthorityEvidence
	}
	return LaneNativeReleasePreparedV1{draft: draft, route: route}, nil
}

// SealFinalWire refuses route and pricing substitutions before the prompt
// verifier sees bytes. It must receive the normal adaptor's final native JSON
// (no re-encoding or terminal response buffer).
func (p LaneNativeReleasePreparedV1) SealFinalWire(ctx context.Context, price LaneNativeChatPreparedPrice, route LaneNativeChatRoute, final []byte,
	wireKey [32]byte, verifier LaneNativeChatPromptBound) (LaneNativeReleaseWireV1, error) {
	if p.route.ChannelID <= 0 || p.route != route || p.draft.price != price {
		return LaneNativeReleaseWireV1{}, model.ErrLaneStoreAuthorityEvidence
	}
	proof, err := p.draft.price.VerifyFinalWire(ctx, p.route, final, wireKey, verifier)
	if err != nil {
		return LaneNativeReleaseWireV1{}, err
	}
	return LaneNativeReleaseWireV1{draft: p.draft, proof: proof}, nil
}

// RevalidateTx is a future binder input, not a binder or a fallback gate.
// Call it inside the exact eventual canonical owner transaction before money
// writes. A dormant read proves neither active escrow nor writer correctness.
// In this version invocations never acquire custody or send an HTTP request.
func (w LaneNativeReleaseWireV1) RevalidateTx(tx *gorm.DB) (LaneStoreBindingDecisionV1, error) {
	return VerifyLaneStoreFinalBindingDraftV1Tx(tx, w.draft, w.proof)
}

func (w LaneNativeReleaseWireV1) Evidence() LaneNativeChatEvidence { return w.proof.Evidence() }
