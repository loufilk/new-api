package plugins_test

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exercise the decoder -> outbound builder boundary; no database or upstream
// request is involved. Billing must use the exact duration the builder sends.
func TestVideoOutboundDurationBounds(t *testing.T) {
	for _, key := range []string{"sora", "doubao"} {
		t.Run(key, func(t *testing.T) {
			name := "sora-2"
			if key == "doubao" {
				name = "doubao-seedance-1-0-pro-250528"
			}
			source, err := builtinplugins.Source(key)
			require.NoError(t, err)
			plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: key})
			require.NoError(t, err)
			for _, protocol := range []string{"openai_responses", "openai_video"} {
				for _, kind := range []string{"json", "multipart"} {
					if protocol == "openai_responses" && kind == "multipart" {
						continue
					}
					for _, quantity := range []any{3601, 0, -2, 5.5, "5x"} {
						t.Run(fmt.Sprintf("%s/%s/%v", protocol, kind, quantity), func(t *testing.T) {
							body := map[string]any{"kind": "json", "value": map[string]any{"model": name, "input": "a cat", "prompt": "a cat", "seconds": quantity}}
							if kind == "multipart" {
								body = map[string]any{"kind": kind, "fields": map[string]any{"model": []string{name}, "prompt": []string{"a cat"}, "seconds": []string{fmt.Sprint(quantity)}}}
							}
							value, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{protocol, "decodeRequest"}, map[string]any{"model": name, "body": body})
							if err == nil {
								request := alibabaObject(t, value)["requestBody"]
								_, err = plugin.Engine.Call(t.Context(), "buildSubmitRequest", map[string]any{"model": name, "upstreamModel": name, "requestBody": request})
							}
							require.Error(t, err, "invalid outbound duration must fail before reservation")
						})
					}
				}
			}

			if key == "doubao" {
				for _, protocol := range []string{"openai_responses", "openai_video"} {
					for _, kind := range []string{"json", "multipart"} {
						if protocol == "openai_responses" && kind == "multipart" {
							continue
						}
						t.Run(protocol+"/"+kind+"/metadata duration", func(t *testing.T) {
							body := map[string]any{"kind": "json", "value": map[string]any{"model": name, "input": "a cat", "prompt": "a cat", "metadata": map[string]any{"duration": 3601}}}
							if kind == "multipart" {
								body = map[string]any{"kind": kind, "fields": map[string]any{"model": []string{name}, "prompt": []string{"a cat"}, "metadata": []string{`{"duration":3601}`}}}
							}
							value, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{protocol, "decodeRequest"}, map[string]any{"model": name, "body": body})
							require.NoError(t, err)
							_, err = plugin.Engine.Call(t.Context(), "buildSubmitRequest", map[string]any{"model": name, "upstreamModel": name, "requestBody": alibabaObject(t, value)["requestBody"]})
							require.Error(t, err)
						})
					}
				}
			}
			if key == "sora" {
				ctx := map[string]any{"model": name, "upstreamModel": name, "requestBody": map[string]any{"prompt": "a cat", "seconds": "8"}}
				value, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
				require.NoError(t, err)
				assert.Equal(t, "8", alibabaObject(t, alibabaObject(t, value)["body"])["seconds"], "Sora's JSON seconds string remains wire-compatible")
				facts, err := plugin.Engine.Call(t.Context(), "extractUsage", ctx)
				require.NoError(t, err)
				assert.Equal(t, 8.0, alibabaObject(t, facts)["seconds"])
			}
			if key != "doubao" {
				return
			}
			for _, duration := range []any{3601, 0, -2, 5.5, "5x"} {
				t.Run(fmt.Sprintf("native/%v", duration), func(t *testing.T) {
					value, err := plugin.Engine.CallPath(t.Context(), "native", []string{"createTask"}, map[string]any{"body": map[string]any{"kind": "json", "value": map[string]any{"model": name, "duration": duration, "content": []any{map[string]any{"type": "text", "text": "a cat"}}}}})
					require.NoError(t, err)
					_, err = plugin.Engine.Call(t.Context(), "buildSubmitRequest", map[string]any{"model": name, "upstreamModel": name, "requestBody": alibabaObject(t, value)["requestBody"]})
					require.Error(t, err)
				})
			}
			for _, seconds := range []any{nil, "5", -1} {
				request := map[string]any{"model": name, "prompt": "a cat", "metadata": map[string]any{"duration": 10, "resolution": "720p"}}
				if seconds != nil {
					request["seconds"] = seconds
				}
				ctx := map[string]any{"model": name, "upstreamModel": name, "requestBody": request}
				value, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
				require.NoError(t, err)
				sent := alibabaObject(t, alibabaObject(t, value)["body"])
				facts, err := plugin.Engine.Call(t.Context(), "extractUsage", ctx)
				require.NoError(t, err)
				want := 10.0
				if seconds == "5" {
					want = 5
				}
				if seconds == -1 {
					want = 12
				}
				expectedDuration := want
				if seconds == -1 {
					expectedDuration = -1
				}
				assert.Equal(t, expectedDuration, sent["duration"])
				assert.Equal(t, want*1280*720*24/1024, alibabaObject(t, facts)["tokens"])
			}
		})
	}
}

func TestVeoOutboundDurationMatchesUsage(t *testing.T) {
	const name = "veo-3.0-generate-001"
	for _, key := range []string{"google", "vertex-ai"} {
		t.Run(key, func(t *testing.T) {
			source, err := builtinplugins.Source(key)
			require.NoError(t, err)
			plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: key})
			require.NoError(t, err)
			for _, value := range []any{3601, 0, -1, 5.5, "5x", 10} {
				t.Run(fmt.Sprint(value), func(t *testing.T) {
					decoded, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_responses", "decodeRequest"}, map[string]any{"model": name, "body": map[string]any{"kind": "json", "value": map[string]any{"model": name, "input": "a cat", "metadata": map[string]any{"durationSeconds": value}}}})
					require.NoError(t, err)
					ctx := map[string]any{"model": name, "upstreamModel": name, "requestBody": alibabaObject(t, decoded)["requestBody"], "baseUrl": "https://provider.example", "upstream": map[string]any{"kind": "new_api"}}
					result, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
					if value != 10 {
						require.Error(t, err)
						return
					}
					require.NoError(t, err)
					sent := alibabaObject(t, alibabaObject(t, result)["body"])
					assert.Equal(t, 10.0, alibabaObject(t, sent["parameters"])["durationSeconds"])
					usage, err := plugin.Engine.Call(t.Context(), "extractUsage", ctx)
					require.NoError(t, err)
					assert.Equal(t, 10.0, alibabaObject(t, usage)["seconds"])
				})
			}
		})
	}
}

func TestHailuoOutboundDurationRejectsBeforeLegacyBilling(t *testing.T) {
	source, err := builtinplugins.Source("hailuo")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "hailuo"})
	require.NoError(t, err)
	for _, seconds := range []any{3601, 0, -1, 5.5, "5x"} {
		t.Run(fmt.Sprint(seconds), func(t *testing.T) {
			const name = "MiniMax-Hailuo-2.3"
			value, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_responses", "decodeRequest"}, map[string]any{"model": name, "body": map[string]any{"kind": "json", "value": map[string]any{"model": name, "input": "a cat", "seconds": seconds}}})
			require.NoError(t, err)
			ctx := map[string]any{"model": name, "upstreamModel": name, "requestBody": alibabaObject(t, value)["requestBody"], "usagePurpose": "billing_ratios"}
			// Legacy billing returns no duration fact; the builder itself must reject.
			_, err = plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
			require.Error(t, err)
		})
	}
}
