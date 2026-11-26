package dto

// NativeCacheReadMeasurement reports whether the provider supplied a
// prompt-cache read count, including an explicit zero. Usage estimates and
// cache writes are not evidence of a cache hit or miss.
func (u *BillingUsage) NativeCacheReadMeasurement() (measured, hit bool) {
	if u == nil || u.Estimated {
		return false, false
	}
	switch {
	case u.ClaudeUsage != nil:
		count := u.ClaudeUsage.CacheReadInputTokens
		return count >= 0 && (u.ClaudeUsage.CacheReadReported || count > 0), count > 0
	case u.GeminiUsageMetadata != nil:
		count := u.GeminiUsageMetadata.CachedContentTokenCount
		return count >= 0 && (u.GeminiUsageMetadata.CacheReadReported || count > 0), count > 0
	case u.OpenAIUsage != nil:
		return u.OpenAIUsage.OpenAICacheReadMeasurement()
	default:
		return false, false
	}
}

// OpenAICacheReadMeasurement reads the flat OpenAI-compatible usage shape.
// A caller must exclude locally estimated usage before using this fallback.
func (u *Usage) OpenAICacheReadMeasurement() (measured, hit bool) {
	if u == nil {
		return false, false
	}
	details := u.PromptTokensDetails
	if u.InputTokensDetails != nil && (u.InputTokensDetails.CacheReadReported || u.InputTokensDetails.CachedTokens > 0) {
		details = *u.InputTokensDetails
	}
	if details.CachedTokens > 0 || u.PromptCacheHitTokens > 0 {
		return true, true
	}
	return details.CacheReadReported && details.CachedTokens == 0, false
}
