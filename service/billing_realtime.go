package service

import (
	"errors"
	"reflect"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

// Observed usage and committed holds are different facts. A failed reserve may
// already have committed, and never makes delivered usage refundable again.
// The held receipt survives process loss for review; it is never auto-refunded.
type realtimeBillingState struct {
	initialHold     int
	free            bool
	segmentTotal    int
	usage           dto.RealtimeUsage
	observed        bool
	actual          int
	channelID       int
	pending         *dto.RealtimeUsage
	pendingTarget   int
	final           bool
	displayEligible bool
	displayClaimed  bool
}

func realtimeActualQuota(info *relaycommon.RelayInfo, usage *dto.RealtimeUsage) (int, *billingexpr.TieredResult) {
	if info.PriceData.FreeModel || usage.TotalTokens == 0 {
		return 0, nil
	}
	var usedVars map[string]bool
	if snap := info.TieredBillingSnapshot; snap != nil {
		usedVars = billingexpr.UsedVarsByHash(snap.ExprString, snap.ExprHash)
	}
	if ok, quota, result := TryTieredSettle(info, BuildTieredTokenParams(usage.AsUsage(), false, usedVars)); ok {
		return quota, result
	}
	modelRatio, _, _ := ratio_setting.GetModelRatio(info.GetBillingModelName())
	quota, clamp := calculateAudioQuota(QuotaInfo{
		InputDetails:  TokenDetails{TextTokens: usage.InputTokenDetails.TextTokens, AudioTokens: usage.InputTokenDetails.AudioTokens},
		OutputDetails: TokenDetails{TextTokens: usage.OutputTokenDetails.TextTokens, AudioTokens: usage.OutputTokenDetails.AudioTokens},
		ModelName:     info.GetBillingModelName(), UsePrice: info.PriceData.UsePrice,
		ModelRatio: modelRatio, ModelPrice: info.PriceData.ModelPrice, GroupRatio: info.PriceData.GroupRatioInfo.GroupRatio,
	})
	noteQuotaClamp(info, clamp)
	return quota, nil
}

func (s *BillingSession) realtimeStateLocked() *realtimeBillingState {
	if s.realtime == nil {
		s.realtime = &realtimeBillingState{initialHold: s.preConsumedQuota, channelID: s.relayInfo.ChannelId}
	}
	return s.realtime
}

// reserveObservedRealtime is deliberately not public Reserve/BeforeSubmit:
// trusted users keep a zero initial hold, but completed response.done usage
// still reserves the same incremental amount as before. No forecast is charged.
func (s *BillingSession) reserveObservedRealtime(usage *dto.RealtimeUsage, segmentQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.durableTerminalSource() {
		return errors.New("realtime requires atomic request billing")
	}
	if s.settled || s.refunded || s.taskOwned {
		return model.ErrBillingFinalized
	}
	if s.realtime != nil && s.realtime.final {
		return errors.New("realtime usage already frozen")
	}
	if segmentQuota < 0 || segmentQuota > common.MaxQuota {
		return errors.New("invalid realtime segment quota")
	}
	r := s.realtimeStateLocked()
	if r.free {
		segmentQuota = 0
	}
	if r.pending == nil {
		next := r.usage.Clone()
		if err := dto.AddRealtimeUsage(&next, usage); err != nil {
			return err
		}
		r.usage, r.observed = next, true
		r.actual, _ = realtimeActualQuota(s.relayInfo, &r.usage)
		if r.free {
			r.actual = 0
		}
		copy := usage.Clone()
		r.pending = &copy
		if segmentQuota > common.MaxQuota-r.initialHold-r.segmentTotal {
			r.pendingTarget = -1
		} else {
			r.pendingTarget = r.initialHold + r.segmentTotal + segmentQuota
		}
	} else if !reflect.DeepEqual(*r.pending, *usage) {
		return errors.New("different realtime segment while prior reservation is unresolved")
	}
	if r.pendingTarget < 0 {
		return errors.New("realtime reservation exceeds quota bound")
	}
	if s.reservationUncertain {
		if err := s.resolveReservationLocked(); err != nil {
			return err
		}
	}
	if s.settlementUncertain {
		if err := s.resolveSettlementBeforeReserveLocked(); err != nil {
			return err
		}
	}
	if err := s.reserveDurableLocked(r.pendingTarget, false); err != nil {
		return err
	}
	r.segmentTotal = r.pendingTarget - r.initialHold
	r.pending = nil
	s.relayInfo.WssPostConsumeSequence++
	return nil
}

func (s *BillingSession) freezeRealtimeUsage(usage *dto.RealtimeUsage, quota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.realtimeStateLocked()
	if r.free && quota != 0 {
		return errors.New("free realtime identity cannot become a paid request")
	}
	var validated dto.RealtimeUsage
	if err := dto.AddRealtimeUsage(&validated, usage); err != nil {
		return err
	}
	if r.observed && !reflect.DeepEqual(r.usage, validated) {
		return errors.New("realtime final usage differs from observed aggregate")
	}
	if r.final && r.actual != quota {
		return errors.New("realtime final amount changed")
	}
	r.usage, r.observed, r.actual, r.final = validated, true, quota, true
	return nil
}

func (s *BillingSession) claimRealtimeDisplay() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.realtime
	if r == nil || !r.displayEligible || r.displayClaimed {
		return false
	}
	r.displayClaimed = true
	return true
}

// PreConsumeFreeRealtimeBilling records zero monetary authorization without
// consulting wallet affordability or reserving a subscription minimum. Free
// requests still own their original token and one final accounting receipt.
func PreConsumeFreeRealtimeBilling(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	if info == nil || !info.PriceData.FreeModel || info.RelayFormat != types.RelayFormatOpenAIRealtime {
		return types.NewError(errors.New("invalid free realtime request"), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	if info.Billing != nil {
		return nil
	}
	funding, err := createFundingSource(moneyport.SourceWallet, moneyport.FactoryRequest{UserID: info.UserId, RequestID: info.RequestId, BillingModel: info.GetBillingModelName(), Amount: 0, AccountOrder: []string{string(model.AccountPaid)}})
	if err != nil {
		return types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
	}
	s := &BillingSession{lang: i18n.StatedLang(c), relayInfo: info, funding: funding, realtime: &realtimeBillingState{free: true}}
	if !s.durableTerminalSource() {
		return types.NewError(errors.New("free realtime requires atomic identity"), types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
	}
	if err := s.reserveDurableLocked(0, true); err != nil {
		return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
	}
	info.Billing = s
	return nil
}
