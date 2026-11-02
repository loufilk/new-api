package service

import (
	"cmp"
	"context"
	"math"
	"slices"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/perf_metrics_setting"
	"github.com/gin-gonic/gin"
)

const tokenRoutingAttemptsKey = "token_routing_attempts"

type tokenRoutingAttempts struct {
	groups   map[string]bool
	channels map[int]bool
}

func tokenRoutingState(c *gin.Context) *tokenRoutingAttempts {
	if value, ok := c.Get(tokenRoutingAttemptsKey); ok {
		return value.(*tokenRoutingAttempts)
	}
	state := &tokenRoutingAttempts{groups: map[string]bool{}, channels: map[int]bool{}}
	c.Set(tokenRoutingAttemptsKey, state)
	return state
}

func tokenRoutingEnabled(c *gin.Context) bool {
	return common.GetContextKeyBool(c, constant.ContextKeyTokenSmartRouting) || common.GetContextKeyBool(c, constant.ContextKeyTokenNoRetryWithinGroup)
}

func tokenRoutingGroups(c *gin.Context, tokenGroup string) []string {
	userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup)
	if common.GetContextKeyBool(c, constant.ContextKeyTokenSmartRouting) || tokenGroup == "auto" {
		return GetRequestAutoGroups(c, userGroup)
	}
	if tokenGroup == "" {
		tokenGroup = userGroup
	}
	if tokenGroup == "" || !IsUserSelectableGroup(userGroup, tokenGroup) {
		return nil
	}
	return []string{tokenGroup}
}

// RelayRetryBudget keeps the legacy global budget unless the key explicitly
// requests one attempt per group. In that mode the finite authorized group list
// is the entire attempt budget, independent of the old per-group retry count.
func RelayRetryBudget(c *gin.Context) int {
	if !common.GetContextKeyBool(c, constant.ContextKeyTokenNoRetryWithinGroup) {
		return common.RetryTimes
	}
	return max(0, len(tokenRoutingGroups(c, common.GetContextKeyString(c, constant.ContextKeyTokenGroup)))-1)
}

// TokenRoutingRetryAvailable is only an additional restriction. It never makes
// an unsafe, canceled, already-streaming, pinned or unknown-delivery error safe.
func TokenRoutingRetryAvailable(c *gin.Context) bool {
	if !tokenRoutingEnabled(c) {
		return true
	}
	// A selected alternative is not permission to replay after downstream output
	// or a canceled client. This also protects task and websocket entry points.
	if c.Request != nil && c.Request.Context().Err() != nil || c.Writer != nil && c.Writer.Written() {
		return false
	}
	groups := tokenRoutingGroups(c, common.GetContextKeyString(c, constant.ContextKeyTokenGroup))
	state := tokenRoutingState(c)
	if common.GetContextKeyBool(c, constant.ContextKeyTokenNoRetryWithinGroup) {
		// An origin-task pin cannot move to another group/channel; do not let
		// its legacy same-channel retry mode defeat the explicit per-key limit.
		if pin, found, _ := GetChannelConstraints(c).ResolvedPin(); found && state.channels[pin.ChannelId] {
			return false
		}
		groups = slices.DeleteFunc(slices.Clone(groups), func(g string) bool { return state.groups[g] })
	}
	if len(groups) == 0 || c.Request == nil {
		return false
	}
	// Only count groups that can actually serve this model and protocol. An
	// unrelated authorized group must not replace the last upstream error with
	// a synthetic model-not-found on a futile extra selection.
	guard, err := model.BeginRequestGroupConfigRead(c)
	if err != nil {
		return false
	}
	defer guard.Close()
	candidates, err := model.GetTokenRoutingCandidates(c.Request.Context(), groups, common.GetContextKeyString(c, constant.ContextKeyOriginalModel), GetChannelConstraints(c).Filters)
	if err != nil || guard.Finish() != nil {
		return false
	}
	return slices.ContainsFunc(candidates, func(r model.RoutingCandidate) bool { return !state.channels[r.Channel.Id] })
}

func markTokenRoutingAttempt(c *gin.Context, channelID int) {
	if !tokenRoutingEnabled(c) {
		return
	}
	state := tokenRoutingState(c)
	group := common.GetContextKeyString(c, constant.ContextKeyAutoGroup)
	if group == "" {
		group = common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	}
	state.groups[group] = true
	state.channels[channelID] = true
}

type scoredTokenRoute struct {
	candidate                    model.RoutingCandidate
	success, price, speed, score float64
}

func selectTokenRoutingChannel(param *RetryParam) (*model.Channel, string, error) {
	c := param.Ctx
	groups := tokenRoutingGroups(c, param.TokenGroup)
	state := tokenRoutingState(c)
	noRetry := common.GetContextKeyBool(c, constant.ContextKeyTokenNoRetryWithinGroup)
	if noRetry {
		groups = slices.DeleteFunc(slices.Clone(groups), func(g string) bool { return state.groups[g] })
	}
	candidates, err := model.GetTokenRoutingCandidates(c.Request.Context(), groups, param.ModelName, GetChannelConstraints(c).Filters)
	if err != nil {
		return nil, "", err
	}
	candidates = slices.DeleteFunc(candidates, func(r model.RoutingCandidate) bool { return state.channels[r.Channel.Id] })
	// Preserve administrator priority tiers within each group. A lower-priority
	// channel becomes eligible only after the higher tier has been attempted.
	priorities := map[string]int64{}
	for _, r := range candidates {
		if p, ok := priorities[r.Group]; !ok || r.Channel.GetPriority() > p {
			priorities[r.Group] = r.Channel.GetPriority()
		}
	}
	candidates = slices.DeleteFunc(candidates, func(r model.RoutingCandidate) bool { return r.Channel.GetPriority() != priorities[r.Group] })
	if len(candidates) == 0 {
		return nil, "", nil
	}
	smart := common.GetContextKeyBool(c, constant.ContextKeyTokenSmartRouting)
	selected := model.RoutingCandidate{}
	if !smart {
		// Manual mode preserves the user's exact order; no sorting by price/health.
		for _, group := range groups {
			choices := make([]model.RoutingCandidate, 0)
			for _, r := range candidates {
				if r.Group == group {
					choices = append(choices, r)
				}
			}
			if len(choices) == 0 {
				continue
			}
			selected = weightedTokenRoute(choices)
			break
		}
	} else {
		metrics := []model.TokenRoutingMetric(nil)
		if perf_metrics_setting.GetSetting().Enabled {
			// Telemetry is advisory, never an authorization source. Missing history
			// uses neutral priors; a metrics-store outage cannot grant another group.
			metricCtx, cancel := context.WithTimeout(c.Request.Context(), 100*time.Millisecond)
			metrics, _ = model.GetTokenRoutingMetrics(metricCtx, param.ModelName, groups)
			cancel()
		}
		selected = rankTokenRoutes(c, candidates, metrics)
	}
	if selected.Channel != nil {
		common.SetContextKey(c, constant.ContextKeyAutoGroup, selected.Group)
	}
	return selected.Channel, selected.Group, nil
}

func weightedTokenRoute(routes []model.RoutingCandidate) model.RoutingCandidate {
	// Use a floating draw to avoid overflow in administrator-provided weights.
	total := float64(0)
	for _, r := range routes {
		total += float64(max(r.Channel.GetWeight(), 0)) + 1
	}
	draw := float64(common.GetRandomInt(1_000_000)) / 1_000_000 * total
	for _, r := range routes {
		draw -= float64(max(r.Channel.GetWeight(), 0)) + 1
		if draw < 0 {
			return r
		}
	}
	return routes[len(routes)-1]
}

func rankTokenRoutes(c *gin.Context, candidates []model.RoutingCandidate, metrics []model.TokenRoutingMetric) model.RoutingCandidate {
	type routeID struct {
		channel int
		group   string
	}
	stats := map[routeID]model.TokenRoutingMetric{}
	for _, m := range metrics {
		stats[routeID{m.ChannelID, m.Group}] = m
	}
	routes := make([]scoredTokenRoute, 0, len(candidates))
	userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup)
	for _, r := range candidates {
		m := stats[routeID{r.Channel.Id, r.Group}]
		success := float64(max(0, m.SuccessCount)+1) / float64(max(0, m.RequestCount)+2)
		speed := math.Inf(1)
		if m.SuccessCount > 0 && m.TotalLatencyMs > 0 {
			speed = float64(m.TotalLatencyMs) / float64(m.SuccessCount)
		}
		price := GetUserGroupRatio(userGroup, r.Group)
		if price < 0 || math.IsNaN(price) || math.IsInf(price, 0) {
			continue
		}
		routes = append(routes, scoredTokenRoute{candidate: r, success: success, price: price, speed: speed})
	}
	if len(routes) == 0 {
		return model.RoutingCandidate{}
	}
	strategy := common.GetContextKeyString(c, constant.ContextKeyTokenRoutingStrategy)
	// Auto balances ranks rather than adding incomparable units. Values are
	// advisory 24-hour health, effective group multiplier and success latency.
	for i := range routes {
		for _, other := range routes {
			if other.success > routes[i].success {
				routes[i].score += 0.5
			}
			if other.price < routes[i].price {
				routes[i].score += 0.3
			}
			if other.speed < routes[i].speed {
				routes[i].score += 0.2
			}
		}
	}
	slices.SortStableFunc(routes, func(a, b scoredTokenRoute) int {
		primary := 0
		switch strategy {
		case "success_rate":
			primary = cmp.Compare(b.success, a.success)
		case "price":
			primary = cmp.Compare(a.price, b.price)
		case "speed":
			primary = cmp.Compare(a.speed, b.speed)
		default:
			primary = cmp.Compare(a.score, b.score)
		}
		if primary != 0 {
			return primary
		}
		if d := cmp.Compare(b.success, a.success); d != 0 {
			return d
		}
		if d := cmp.Compare(a.price, b.price); d != 0 {
			return d
		}
		if d := cmp.Compare(a.speed, b.speed); d != 0 {
			return d
		}
		if d := cmp.Compare(a.candidate.Group, b.candidate.Group); d != 0 {
			return d
		}
		return cmp.Compare(a.candidate.Channel.Id, b.candidate.Channel.Id)
	})
	// Preserve weighted distribution for true score ties within the winning group.
	best := routes[0]
	ties := []model.RoutingCandidate{best.candidate}
	for _, r := range routes[1:] {
		if r.candidate.Group == best.candidate.Group && r.score == best.score && r.success == best.success && r.price == best.price && r.speed == best.speed {
			ties = append(ties, r.candidate)
		}
	}
	return weightedTokenRoute(ties)
}

func recordTokenRoutingOutcome(c *gin.Context, info *relaycommon.RelayInfo, apiErr *types.NewAPIError) {
	if c == nil || c.Request == nil || model.DB == nil || !perf_metrics_setting.GetSetting().Enabled {
		return
	}
	state := RequestPolicy(c)
	if state.RoutingOutcomeRecorded || state.AttemptChannelID <= 0 {
		return
	}
	state.RoutingOutcomeRecorded = true
	outcome := perfmetrics.ClassifyRelayOutcome(c.Request.Context(), info, apiErr)
	if outcome == perfmetrics.OutcomeIgnored {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 100*time.Millisecond)
	defer cancel()
	// A failed observation must never change request delivery or financial state.
	_ = model.RecordTokenRoutingMetric(ctx, state.AttemptChannelID, common.GetContextKeyString(c, constant.ContextKeyOriginalModel), state.SelectedGroup, outcome == perfmetrics.OutcomeSuccess, time.Since(state.AttemptStartedAt))
}
