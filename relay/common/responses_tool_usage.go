package common

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// ResponsesToolUsageObserver shares per-call accounting across native and
// converted Responses transports without retaining generated text. Completed
// stream items and the cumulative terminal output describe the same calls.
type ResponsesToolUsageObserver struct {
	info         *RelayInfo
	seen         map[string]struct{}
	images       ImageGenerationCallCounter
	vendorItems  []dto.ResponsesOutput
	vendorCounts map[string]int
	finished     bool
}

func NewResponsesToolUsageObserver(info *RelayInfo) *ResponsesToolUsageObserver {
	return &ResponsesToolUsageObserver{info: info, seen: make(map[string]struct{})}
}

func (o *ResponsesToolUsageObserver) Observe(event *dto.ResponsesStreamResponse, raw []byte) {
	if o == nil || o.finished || event == nil {
		return
	}
	switch event.Type {
	case dto.ResponsesOutputTypeItemDone:
		o.observeItem(event.Item, event.OutputIndex)
	case "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled", "response.error":
		o.ObserveResponse(event.Response, raw)
	}
}

func (o *ResponsesToolUsageObserver) ObserveResponse(response *dto.OpenAIResponsesResponse, raw []byte) {
	if o == nil || o.finished {
		return
	}
	if response != nil {
		for i := range response.Output {
			o.observeItem(&response.Output[i], &i)
		}
	}
	if o.info != nil && o.info.VendorToolUsage != nil && len(raw) > 0 {
		for name, count := range o.info.VendorToolUsage(raw) {
			if o.vendorCounts == nil {
				o.vendorCounts = make(map[string]int)
			}
			o.vendorCounts[name] = count
		}
	}
}

func (o *ResponsesToolUsageObserver) observeItem(item *dto.ResponsesOutput, outputIndex *int) {
	if item == nil || o.info == nil {
		return
	}
	if item.Type == dto.ResponsesOutputTypeImageGenerationCall {
		o.images.Observe(item, outputIndex)
		return
	}
	switch item.Type {
	case dto.BuildInCallWebSearchCall, dto.BuildInCallFileSearchCall, dto.BuildInCallFunctionCall:
	default:
		return
	}
	aliases := make([]string, 0, 3)
	if item.ID != "" {
		aliases = append(aliases, item.Type+":id:"+item.ID)
	}
	if item.CallId != "" {
		aliases = append(aliases, item.Type+":call:"+item.CallId)
	}
	if outputIndex != nil && *outputIndex >= 0 {
		aliases = append(aliases, fmt.Sprintf("%s:index:%d", item.Type, *outputIndex))
	}
	if len(aliases) == 0 {
		// Some compatible endpoints omit both identity fields. A stable
		// payload identity still prevents replayed done items charging twice.
		encoded, err := common.Marshal(item)
		if err != nil {
			return
		}
		aliases = append(aliases, fmt.Sprintf("%s:payload:%x", item.Type, sha256.Sum256(encoded)))
	}
	duplicate := false
	for _, alias := range aliases {
		_, seen := o.seen[alias]
		duplicate = duplicate || seen
		o.seen[alias] = struct{}{}
	}
	if duplicate {
		return
	}
	if o.info.VendorToolUsage != nil && item.Type == dto.BuildInCallWebSearchCall {
		// The vendor can distinguish Search from Fetch even when the stream
		// ends before a cumulative terminal. Do not keep unrelated text or
		// image payloads for this fallback.
		o.vendorItems = append(o.vendorItems, dto.ResponsesOutput{
			Type: item.Type, ID: item.ID, Status: item.Status, Action: item.Action,
		})
	}
	switch strings.ToLower(strings.TrimSpace(item.Status)) {
	case "failed", "cancelled", "canceled", "incomplete", "partial", "in_progress", "searching":
		return
	}
	o.info.CountBillableToolCall(item.Type, item.Name)
}

// Finish commits observed calls once. Vendor cumulative counts, including
// explicit zeros, take precedence over per-item fallback counts.
func (o *ResponsesToolUsageObserver) Finish() {
	if o == nil || o.finished || o.info == nil {
		return
	}
	o.finished = true
	if len(o.vendorItems) > 0 {
		body, err := common.Marshal(struct {
			Output []dto.ResponsesOutput `json:"output"`
		}{Output: o.vendorItems})
		if err == nil {
			o.info.ApplyVendorToolUsage(body)
		}
	}
	for name, count := range o.vendorCounts {
		o.info.SetBillableToolCount(name, count)
	}
	o.images.Commit(o.info)
}

// HasCalls distinguishes completed tool work from a role-only failed stream.
// Pricing remains owned by settlement; zero token usage can still carry a
// per-call surcharge.
func (o *ResponsesToolUsageObserver) HasCalls() bool {
	if o == nil || o.info == nil || o.info.ResponsesUsageInfo == nil {
		return false
	}
	for _, tool := range o.info.ResponsesUsageInfo.BuiltInTools {
		if tool != nil && tool.CallCount > 0 {
			return true
		}
	}
	return false
}
