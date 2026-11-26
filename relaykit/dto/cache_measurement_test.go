package dto

import (
	"encoding/json"
	"testing"
)

func TestNativeCacheReadMeasurement(t *testing.T) {
	for _, tc := range []struct {
		name, body    string
		measured, hit bool
	}{
		{"openai chat explicit zero", `{"source":"oai_chat","openai_usage":{"prompt_tokens":8,"prompt_tokens_details":{"cached_tokens":0}}}`, true, false},
		{"openai response hit", `{"source":"oai_responses","openai_usage":{"input_tokens":8,"input_tokens_details":{"cached_tokens":3}}}`, true, true},
		{"openai missing", `{"source":"oai_chat","openai_usage":{"prompt_tokens":8,"prompt_tokens_details":{"cache_write_tokens":4}}}`, false, false},
		{"openai null", `{"openai_usage":{"prompt_tokens_details":{"cached_tokens":null}}}`, false, false},
		{"claude explicit zero", `{"claude_usage":{"input_tokens":5,"cache_creation_input_tokens":8,"cache_read_input_tokens":0}}`, true, false},
		{"claude hit", `{"claude_usage":{"cache_read_input_tokens":20}}`, true, true},
		{"claude missing", `{"claude_usage":{"cache_creation_input_tokens":8}}`, false, false},
		{"gemini explicit zero", `{"gemini_usage_metadata":{"promptTokenCount":50,"cachedContentTokenCount":0}}`, true, false},
		{"gemini invalid", `{"gemini_usage_metadata":{"promptTokenCount":50,"cachedContentTokenCount":-1}}`, false, false},
		{"gemini estimated", `{"estimated":true,"gemini_usage_metadata":{"promptTokenCount":50,"cachedContentTokenCount":10}}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var u BillingUsage
			if err := json.Unmarshal([]byte(tc.body), &u); err != nil {
				t.Fatal(err)
			}
			gotMeasured, gotHit := CloneBillingUsage(&u).NativeCacheReadMeasurement()
			if gotMeasured != tc.measured || gotHit != tc.hit {
				t.Fatalf("got measured=%v hit=%v, want measured=%v hit=%v", gotMeasured, gotHit, tc.measured, tc.hit)
			}
		})
	}
	var a, b BillingUsage
	for dst, body := range map[*BillingUsage]string{&a: `{"source":"oai_responses","openai_usage":{"input_tokens":8,"input_tokens_details":{"cached_tokens":0}}}`, &b: `{"source":"oai_responses","openai_usage":{"output_tokens":2}}`} {
		if err := json.Unmarshal([]byte(body), dst); err != nil {
			t.Fatal(err)
		}
	}
	if measured, hit := MergeBillingUsageNonZero(&a, &b).NativeCacheReadMeasurement(); !measured || hit {
		t.Fatal("later output-only snapshot erased the measured miss")
	}
}
