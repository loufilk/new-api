package service

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

const laneEligibilityObservationKey = "lane_eligibility_observation"

// LaneEligibilityObservation is a request-local, read-only funding observation,
// not admission authority. Every field has a bounded vocabulary; no identifiers,
// balances, request bodies or credentials are retained or logged. Nothing reads
// this value to choose a billing or relay path.
//
// The paid-wallet class is only a snapshot of the existing billing reads. It is
// NOT proof that rewards, promotions or policy cannot change while waiting.
// Production activation still requires a shared ordinary/lane request owner,
// fenced existing-Key generations, logical wallet/Key balances and reclaim,
// transactionally revalidated funding, pricing/transport parity and safe drain.
// The tagged, fixed-Key, loopback-only laboratory is not that implementation.
type LaneEligibilityObservation struct {
	Mode       string
	Preference string
	Funding    string
	Key        string
	Route      string
	Gate       string
}

// BeginLaneEligibilityObservation opts an internal caller into request-local
// diagnostics. No request header, environment flag or public API enables this.
// Normal relay requests do not allocate or retain an observation.
func BeginLaneEligibilityObservation(c *gin.Context) {
	if c != nil {
		c.Set(laneEligibilityObservationKey, LaneEligibilityObservation{
			Mode: "observe_only", Funding: "unresolved", Key: "unsupported",
			Route: "other", Gate: "production_bridge_unavailable",
		})
	}
}

// LaneEligibilityForRequest returns a value copy. Absence or any observation
// must never be interpreted as permission to reserve, dispatch or fall back.
func LaneEligibilityForRequest(c *gin.Context) (LaneEligibilityObservation, bool) {
	if c == nil {
		return LaneEligibilityObservation{}, false
	}
	value, ok := c.Get(laneEligibilityObservationKey)
	observation, valid := value.(LaneEligibilityObservation)
	return observation, ok && valid
}

// observeLaneFunding uses only values already read by NewBillingSession. It
// adds no SQL, Redis, configuration lookups, background work or error path.
// Observe the selected attempt before pre-consume; ordinary funding continues
// to own its existing transaction, fallback rules and ambiguous-outcome policy.
func observeLaneFunding(c *gin.Context, info *relaycommon.RelayInfo, source string, order []model.LedgerAccount, paid, bonus, reward int) {
	if _, enabled := LaneEligibilityForRequest(c); !enabled {
		return
	}
	observation := LaneEligibilityObservation{
		Mode: "observe_only", Funding: "unresolved", Key: "unsupported",
		Route: "other", Gate: "production_bridge_unavailable",
	}
	if info != nil {
		observation.Preference = common.NormalizeBillingPreference(info.UserSetting.BillingPreference)
		if info.TokenId > 0 && !info.TokenUnlimited && !info.IsPlayground {
			observation.Key = "finite"
		}
		if c.Request != nil && c.Request.URL != nil && c.Request.Method == http.MethodPost &&
			c.Request.URL.EscapedPath() == "/v1/chat/completions" && c.Request.URL.RawQuery == "" &&
			info.TokenGroup != "auto" && info.UsingGroup != "auto" &&
			!common.GetContextKeyBool(c, constant.ContextKeyTokenSmartRouting) {
			// This describes the route family, not validated transport support.
			observation.Route = "chat_route_family"
		}
	}
	switch source {
	case BillingSourceSubscription:
		observation.Funding = "subscription"
	case BillingSourceWallet:
		observation.Funding = laneWalletFundingClass(order, paid, bonus, reward)
	}
	c.Set(laneEligibilityObservationKey, observation)
}

func laneWalletFundingClass(order []model.LedgerAccount, paid, bonus, reward int) string {
	if len(order) == 0 || paid < 0 || bonus < 0 || reward < 0 {
		return "unresolved"
	}
	paidAllowed, nonPaidAvailable := false, false
	for _, account := range order {
		switch account {
		case model.AccountPaid:
			paidAllowed = true
		case model.AccountBonus:
			nonPaidAvailable = nonPaidAvailable || bonus > 0
		case model.AccountReward:
			nonPaidAvailable = nonPaidAvailable || reward > 0
		default:
			return "unresolved"
		}
	}
	if nonPaidAvailable {
		return "non_paid_wallet"
	}
	if paidAllowed && paid > 0 {
		return "paid_wallet_snapshot"
	}
	return "unresolved"
}
