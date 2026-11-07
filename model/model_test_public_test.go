package model

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelTestPublicFingerprintWhitelist(t *testing.T) {
	run := ModelTestRun{ID: "public-id", Source: "user", UserID: 11, TokenID: 22, RequestID: "private-request", ChannelID: 33, ChannelKeyIndex: 44, SamplesJSON: "private-samples", ArtworkHTML: "private-artwork", ErrorCode: "private-error", FingerprintResult: JSONValue(`{"status":"scored","used_outputs":3,"token_id":22,"api_key":"private-key","raw_response":"private-response","top_candidates":[{"model":"gpt-test","request_id":"private-nested"}],"sample_quality":[{"accepted":true,"raw_request":"private-prompt"}]}`)}
	body, err := common.Marshal(run.PublicFingerprint())
	require.NoError(t, err)
	var row map[string]any
	require.NoError(t, common.Unmarshal(body, &row))
	assert.Len(t, row, 10)
	for _, field := range []string{"id", "source", "model", "group", "benchmark_id", "status", "assessment", "fingerprint_result", "created_at", "completed_at"} {
		assert.Contains(t, row, field)
	}
	assert.NotContains(t, string(body), "private-")
	report := row["fingerprint_result"].(map[string]any)
	assert.Equal(t, "scored", report["status"])
	assert.Equal(t, float64(3), report["used_outputs"])
	assert.NotContains(t, report, "token_id")
	assert.NotContains(t, report, "api_key")
	assert.NotContains(t, report["top_candidates"].([]any)[0], "request_id")
	assert.NotContains(t, report["sample_quality"].([]any)[0], "raw_request")
	for _, invalid := range []string{"", "not-json", `{"status":17}`, `[]`} {
		run.FingerprintResult = JSONValue(invalid)
		encoded, err := common.Marshal(run.PublicFingerprint())
		require.NoError(t, err)
		require.NoError(t, common.Unmarshal(encoded, &row))
		assert.Nil(t, row["fingerprint_result"])
	}
}

func TestListPublicModelTestRunsCrossUserAndLimits(t *testing.T) {
	ctx, config, _ := fingerprintSiteFixture(t)
	prefix := uuid.NewString()[:8]
	modelName := "public-" + prefix
	config.Targets = []ModelFingerprintSiteTarget{{Group: "default", Model: modelName, Enabled: true}, {Group: "premium", Model: modelName, Enabled: true}}
	_, err := UpdateModelFingerprintSiteConfig(ctx, config.Version, 7, *config)
	require.NoError(t, err)
	runs := make([]ModelTestRun, 0)
	for i := range 61 {
		runs = append(runs, ModelTestRun{ID: fmt.Sprintf("%s-score-%02d", prefix, i), Source: "user", UserID: i%2 + 1, Model: modelName, Group: "default", BenchmarkID: "local-fingerprint-v1", Status: "completed", Assessment: "scored", CreatedAt: int64(i + 1), CompletedAt: int64(i + 2), FingerprintResult: JSONValue(`{"status":"scored"}`)})
	}
	for i := range 7 {
		runs = append(runs, ModelTestRun{ID: fmt.Sprintf("%s-fail-%d", prefix, i), Source: "user", UserID: 2, Model: modelName, Group: "default", BenchmarkID: "local-fingerprint-v1", Status: "failed", CreatedAt: int64(100 + i)})
	}
	for _, tc := range []struct{ name, group, source, benchmark, status, assessment string }{
		{"other-group", "premium", "user", "local-fingerprint-v1", "completed", "scored"},
		{"running", "default", "user", "local-fingerprint-v1", "running", "unassessed"},
		{"legacy", "default", "user", "pelican-static-v1", "completed", "scored"},
		{"site", "default", "site", "local-fingerprint-v1", "completed", "scored"},
		{"unscored", "default", "user", "local-fingerprint-v1", "completed", "unscorable"},
	} {
		runs = append(runs, ModelTestRun{ID: prefix + "-" + tc.name, Source: tc.source, UserID: 2, Model: modelName, Group: tc.group, BenchmarkID: tc.benchmark, Status: tc.status, Assessment: tc.assessment, CreatedAt: 200})
	}
	require.NoError(t, DB.Create(&runs).Error)
	public, err := ListPublicModelTestRuns(context.Background())
	require.NoError(t, err)
	ids := make([]string, 0)
	for _, run := range public {
		if run.Model == modelName {
			ids = append(ids, run.ID)
			assert.Equal(t, "user", run.Source)
		}
	}
	require.Len(t, ids, 61)
	assert.Contains(t, ids, prefix+"-score-09") // owned by the other user
	assert.NotContains(t, ids, prefix+"-score-00")
	assert.Contains(t, ids, prefix+"-other-group")
	assert.Contains(t, ids, prefix+"-unscored")
	for _, suffix := range []string{"running", "legacy", "site", "score-08"} {
		assert.NotContains(t, ids, prefix+"-"+suffix)
	}
	for _, suffix := range []string{"fail-0", "fail-1", "fail-2"} {
		assert.Contains(t, ids, prefix+"-"+suffix, "recent failures occupy the same bounded result window")
	}
	_, err = GetModelTestRun(1, prefix+"-score-01")
	require.Error(t, err)
	_, err = CancelModelTestRun(1, prefix+"-running")
	require.Error(t, err)
	own, err := GetModelTestRun(2, prefix+"-running")
	require.NoError(t, err)
	assert.False(t, own.CancelRequested)
	personal, err := ListModelTestRuns(1)
	require.NoError(t, err)
	for _, run := range personal {
		if strings.HasPrefix(run.ID, prefix) {
			assert.Equal(t, 1, run.UserID)
		}
	}
}
