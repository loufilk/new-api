package dto

import (
	"errors"
	"math"
)

func (u RealtimeUsage) Clone() RealtimeUsage {
	u.InputTokenDetails = u.InputTokenDetails.Clone()
	return u
}

// AddRealtimeUsage preserves every provider counter and presence bit. It either
// publishes the complete aggregate or leaves it untouched on invalid/overflowing
// input. Cache creation is normalized per segment, not max(sum(a), sum(b)).
func AddRealtimeUsage(total, segment *RealtimeUsage) error {
	if total == nil || segment == nil {
		return errors.New("missing realtime usage")
	}
	segmentCopy := segment.Clone()
	if segmentCopy.InputTokens < 0 || segmentCopy.OutputTokens < 0 || segmentCopy.InputTokens > math.MaxInt-segmentCopy.OutputTokens {
		return errors.New("invalid realtime input/output totals")
	}
	measured := segmentCopy.InputTokens + segmentCopy.OutputTokens
	if segmentCopy.TotalTokens == 0 && measured > 0 {
		segmentCopy.TotalTokens = measured
	}
	if segmentCopy.TotalTokens != measured {
		return errors.New("inconsistent realtime usage totals")
	}
	if measured == 0 && (segmentCopy.InputTokenDetails.TextTokens != 0 || segmentCopy.InputTokenDetails.AudioTokens != 0 || segmentCopy.InputTokenDetails.ImageTokens != 0 || segmentCopy.InputTokenDetails.CachedTokens != 0 || segmentCopy.InputTokenDetails.CacheCreationTokensTotal() != 0 || segmentCopy.OutputTokenDetails.TextTokens != 0 || segmentCopy.OutputTokenDetails.AudioTokens != 0 || segmentCopy.OutputTokenDetails.ImageTokens != 0 || segmentCopy.OutputTokenDetails.ReasoningTokens != 0) {
		return errors.New("realtime detail usage has no authoritative total")
	}
	segment = &segmentCopy
	next := total.Clone()
	add := func(dst *int, value int) error {
		if *dst < 0 || value < 0 || *dst > math.MaxInt-value {
			return errors.New("invalid or overflowing realtime usage")
		}
		*dst += value
		return nil
	}
	pairs := []struct {
		dst   *int
		value int
	}{
		{&next.TotalTokens, segment.TotalTokens}, {&next.InputTokens, segment.InputTokens}, {&next.OutputTokens, segment.OutputTokens},
		{&next.InputTokenDetails.CachedTokens, segment.InputTokenDetails.CachedTokens},
		{&next.InputTokenDetails.CachedCreationTokens, segment.InputTokenDetails.CachedCreationTokens},
		{&next.InputTokenDetails.CacheWriteTokens, segment.InputTokenDetails.CacheWriteTokens},
		{&next.InputTokenDetails.TextTokens, segment.InputTokenDetails.TextTokens},
		{&next.InputTokenDetails.AudioTokens, segment.InputTokenDetails.AudioTokens},
		{&next.InputTokenDetails.ImageTokens, segment.InputTokenDetails.ImageTokens},
		{&next.OutputTokenDetails.TextTokens, segment.OutputTokenDetails.TextTokens},
		{&next.OutputTokenDetails.AudioTokens, segment.OutputTokenDetails.AudioTokens},
		{&next.OutputTokenDetails.ImageTokens, segment.OutputTokenDetails.ImageTokens},
		{&next.OutputTokenDetails.ReasoningTokens, segment.OutputTokenDetails.ReasoningTokens},
	}
	for _, pair := range pairs {
		if err := add(pair.dst, pair.value); err != nil {
			return err
		}
	}
	if d := segment.InputTokenDetails.CachedTokensDetails; d != nil {
		if next.InputTokenDetails.CachedTokensDetails == nil {
			next.InputTokenDetails.CachedTokensDetails = &CachedTokenDetails{}
		}
		out := next.InputTokenDetails.CachedTokensDetails
		for _, pair := range []struct {
			dst   **int
			value *int
		}{{&out.TextTokens, d.TextTokens}, {&out.ImageTokens, d.ImageTokens}, {&out.AudioTokens, d.AudioTokens}} {
			if pair.value == nil {
				continue
			}
			if *pair.dst == nil {
				*pair.dst = new(int)
			}
			if err := add(*pair.dst, *pair.value); err != nil {
				return err
			}
		}
	}
	// A cached-modality count is complete only if every relevant response
	// reported it. Unknown must not turn into an apparent zero or be resurrected
	// by a later response that happens to report that modality.
	unknown := func(u RealtimeUsage) uint8 {
		mask := u.cacheDetailsUnknown
		// Once normalized, cross-response co-occurrence is not evidence:
		// an image-only/no-cache response and a text-only/cache response must
		// not manufacture a missing image-cache report in their aggregate.
		if u.cacheCreationNormalized {
			return mask
		}
		d := u.InputTokenDetails
		if d.CachedTokens <= 0 {
			return mask
		}
		c := d.CachedTokensDetails
		if u.InputTokens > 0 && (c == nil || c.TextTokens == nil) {
			mask |= 1
		}
		if d.ImageTokens > 0 && (c == nil || c.ImageTokens == nil) {
			mask |= 2
		}
		if d.AudioTokens > 0 && (c == nil || c.AudioTokens == nil) {
			mask |= 4
		}
		return mask
	}
	next.cacheDetailsUnknown = unknown(*total) | unknown(*segment)
	if d := next.InputTokenDetails.CachedTokensDetails; d != nil {
		if next.cacheDetailsUnknown&1 != 0 {
			d.TextTokens = nil
		}
		if next.cacheDetailsUnknown&2 != 0 {
			d.ImageTokens = nil
		}
		if next.cacheDetailsUnknown&4 != 0 {
			d.AudioTokens = nil
		}
	}
	created := total.InputTokenDetails.CacheCreationTokensTotal()
	if total.cacheCreationNormalized {
		created = total.cacheCreationTotal
	}
	segmentCreated := segment.InputTokenDetails.CacheCreationTokensTotal()
	if segment.cacheCreationNormalized {
		segmentCreated = segment.cacheCreationTotal
	}
	if err := add(&created, segmentCreated); err != nil {
		return err
	}
	next.cacheCreationTotal, next.cacheCreationNormalized = created, true
	*total = next
	return nil
}

// AsUsage feeds the existing opt-in expression normalizer. Raw realtime
// counters remain intact; only the billing view normalizes equivalent cache
// creation names after aggregation.
func (u RealtimeUsage) AsUsage() *Usage {
	input := u.InputTokenDetails.Clone()
	if u.cacheCreationNormalized {
		input.CachedCreationTokens, input.CacheWriteTokens = u.cacheCreationTotal, 0
	}
	return &Usage{PromptTokens: u.InputTokens, CompletionTokens: u.OutputTokens, TotalTokens: u.TotalTokens,
		InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, PromptTokensDetails: input,
		CompletionTokenDetails: u.OutputTokenDetails}
}
