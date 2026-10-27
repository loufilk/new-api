package billingexpr

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFreezeRequestInputCapturesOnlyReferencedFacts(t *testing.T) {
	exprStr := `tier("base", 1) * (header("x-plan") == "fast" ? 2 : 1) * (param("quality") == "hd" ? 3 : 1)`
	input := RequestInput{
		Headers: map[string]string{
			"X-Plan":        "fast",
			"X-Unused":      "do-not-store",
			"Authorization": "Bearer do-not-store",
		},
		Body: []byte(`{"quality":"hd","secret":"do-not-store"}`),
	}

	frozen, err := FreezeRequestInput(exprStr, input)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"x-plan": "fast"}, frozen.Headers)
	assert.Equal(t, map[string]any{"quality": "hd"}, frozen.Params)
	assert.Empty(t, frozen.Body)
	assert.NotZero(t, frozen.Timestamp)

	cost, _, err := RunExprWithRequest(exprStr, TokenParams{}, frozen)
	require.NoError(t, err)
	assert.Equal(t, 6.0, cost)

	encoded, err := common.Marshal(frozen)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "do-not-store")
	assert.NotContains(t, string(encoded), "authorization")
}

func TestFreezeRequestInputRejectsSensitiveReferencedHeader(t *testing.T) {
	_, err := FreezeRequestInput(
		`tier("secret", header("authorization") == "Bearer x" ? 2 : 1)`,
		RequestInput{Headers: map[string]string{"Authorization": "Bearer x"}},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sensitive")
}

func TestFreezeRequestInputRejectsSensitiveOrContentParamPaths(t *testing.T) {
	for _, path := range []string{
		"api_key",
		"auth.token",
		"messages.0.content",
		"metadata",
	} {
		t.Run(path, func(t *testing.T) {
			_, err := FreezeRequestInput(
				fmt.Sprintf(`tier("guard", param(%q) == nil ? 1 : 2)`, path),
				RequestInput{Body: []byte(`{}`)},
			)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "cannot be persisted")
		})
	}
}

func TestFreezeRequestInputRejectsComplexOrCredentialLikeParamValues(t *testing.T) {
	tests := []struct {
		name  string
		expr  string
		input RequestInput
	}{
		{
			name:  "object value",
			expr:  `tier("guard", param("options") == nil ? 1 : 2)`,
			input: RequestInput{Body: []byte(`{"options":{"quality":"hd"}}`)},
		},
		{
			name:  "advanced selector",
			expr:  `tier("guard", param("items.#") == nil ? 1 : 2)`,
			input: RequestInput{Body: []byte(`{"items":[1,2]}`)},
		},
		{
			name:  "credential shaped string",
			expr:  `tier("guard", param("mode") == nil ? 1 : 2)`,
			input: RequestInput{Body: []byte(`{"mode":"Bearer abc123"}`)},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := FreezeRequestInput(testCase.expr, testCase.input)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "cannot be persisted")
		})
	}
}

func TestFreezeRequestInputAllowsSafeNestedScalarParam(t *testing.T) {
	exprStr := `tier("safe", param("stream_options.fast_mode") == true ? 2.0 : 1.0)`
	frozen, err := FreezeRequestInput(exprStr, RequestInput{
		Body: []byte(`{"stream_options":{"fast_mode":true},"prompt":"never persisted"}`),
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"stream_options.fast_mode": true}, frozen.Params)

	encoded, err := common.Marshal(frozen)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "never persisted")
	cost, _, err := RunExprWithRequest(exprStr, TokenParams{}, frozen)
	require.NoError(t, err)
	assert.Equal(t, 2.0, cost)
}

func TestFrozenRequestTimestampKeepsTimeRuleStable(t *testing.T) {
	exprStr := `tier("time", hour("UTC") == 3 ? 2.0 : 1.0)`
	frozen, err := FreezeRequestInput(exprStr, RequestInput{
		Timestamp: time.Date(2026, time.September, 10, 3, 15, 0, 0, time.UTC).Unix(),
	})
	require.NoError(t, err)

	cost, _, err := RunExprWithRequest(exprStr, TokenParams{}, frozen)
	require.NoError(t, err)
	assert.Equal(t, 2.0, cost)
}

func TestFrozenImageQuantitySurvivesPersistenceWithoutChangingRequestN(t *testing.T) {
	expression := `tier("images", fixed(0.01)) * image_count * (param("n") == 2 ? 3 : 1)`
	count := 4
	frozen, err := FreezeRequestInput(expression, RequestInput{Body: []byte(`{"n":2}`), ImageCount: &count})
	require.NoError(t, err)
	count = 8 // The original request owner may reuse its quantity storage.
	encoded, err := common.Marshal(frozen)
	require.NoError(t, err)
	var restored RequestInput
	require.NoError(t, common.Unmarshal(encoded, &restored))
	cost, trace, err := RunExprWithRequest(expression, TokenParams{}, restored)
	require.NoError(t, err)
	assert.Equal(t, 120000.0, cost)
	require.NotNil(t, trace.ImageCount)
	assert.Equal(t, 4, *trace.ImageCount)
	assert.Equal(t, float64(2), restored.Params["n"])
}
