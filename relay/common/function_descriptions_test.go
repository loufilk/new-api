package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureFunctionDescriptions(t *testing.T) {
	override := map[string]any{"operations": []any{map[string]any{
		"mode": "ensure_function_descriptions", "value": "Client-provided function.",
	}}}
	for _, tc := range []struct {
		name, input, want string
	}{
		{
			name:  "responses mixed tool types and every description state",
			input: `{"tools":[{"type":"function","name":"missing","parameters":{"type":"object","properties":{"z":{"type":"string"},"a":{"type":"string"}}},"strict":false},{"type":"web_search"},{"type":"function","name":"null","description":null},{"type":"function","name":"empty","description":""},{"type":"function","name":"existing","description":" Keep exactly. "},{"type":"image_generation"},{"type":"function","name":"spaces","description":"  "},{"type":"function","name":"invalid","description":7},{"type":"custom","name":"custom"},{"type":"namespace","name":"ns","tools":[{"type":"function","name":"nested"}]}]}`,
			want:  `{"tools":[{"type":"function","name":"missing","parameters":{"type":"object","properties":{"z":{"type":"string"},"a":{"type":"string"}}},"strict":false,"description":"Client-provided function."},{"type":"web_search"},{"type":"function","name":"null","description":"Client-provided function."},{"type":"function","name":"empty","description":"Client-provided function."},{"type":"function","name":"existing","description":" Keep exactly. "},{"type":"image_generation"},{"type":"function","name":"spaces","description":"  "},{"type":"function","name":"invalid","description":7},{"type":"custom","name":"custom"},{"type":"namespace","name":"ns","tools":[{"type":"function","name":"nested"}]}]}`,
		},
		{
			name:  "chat tools and legacy functions",
			input: `{"tools":[{"type":"function","function":{"name":"missing","parameters":{"type":"object"}}},{"type":"function","function":{"name":"existing","description":"Original"}},{"function":{"name":"implicit"}},{"type":"web_search","function":{"name":"not_a_function"}}],"functions":[{"name":"legacy","description":null},{"name":"kept","description":"Legacy description"}]}`,
			want:  `{"tools":[{"type":"function","function":{"name":"missing","parameters":{"type":"object"},"description":"Client-provided function."}},{"type":"function","function":{"name":"existing","description":"Original"}},{"function":{"name":"implicit","description":"Client-provided function."}},{"type":"web_search","function":{"name":"not_a_function"}}],"functions":[{"name":"legacy","description":"Client-provided function."},{"name":"kept","description":"Legacy description"}]}`,
		},
		{
			name:  "do not manufacture malformed definitions",
			input: `{"tools":[null,"function",{"type":"function"},{"type":"function","name":8},{"type":"function","name":""},{"type":"function","function":null},{"type":"function","function":{"name":null}},{"name":"untyped_flat"}],"functions":[null,{},8,{"name":false}]}`,
			want:  `{"tools":[null,"function",{"type":"function"},{"type":"function","name":8},{"type":"function","name":""},{"type":"function","function":null},{"type":"function","function":{"name":null}},{"name":"untyped_flat"}],"functions":[null,{},8,{"name":false}]}`,
		},
		{name: "no tools preserves original bytes", input: "{\n  \"model\": \"gpt-test\", \"input\": \"hello\"\n}", want: "{\n  \"model\": \"gpt-test\", \"input\": \"hello\"\n}"},
		{name: "invalid containers remain invalid", input: `{"tools":{"type":"function","name":"bad"},"functions":"bad"}`, want: `{"tools":{"type":"function","name":"bad"},"functions":"bad"}`},
		{name: "numeric object keys do not become array indices", input: `{"tools":{"type":"function","name":"outer","0":{"name":"nested"}},"functions":{"0":{"name":"legacy"}}}`, want: `{"tools":{"type":"function","name":"outer","0":{"name":"nested"}},"functions":{"0":{"name":"legacy"}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ApplyParamOverride([]byte(tc.input), override, nil)
			require.NoError(t, err)
			assert.Equal(t, tc.want, string(got), "unrelated bytes, including schema property order, must not change")
			second, err := ApplyParamOverride(got, override, nil)
			require.NoError(t, err)
			assert.Equal(t, got, second, "the compatibility operation must be idempotent")
		})
	}
}

func TestEnsureFunctionDescriptionsRequiresExplicitValidConfiguration(t *testing.T) {
	input := []byte(`{"tools":[{"type":"function","name":"lookup"}]}`)
	unchanged, err := ApplyParamOverride(input, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, input, unchanged, "no global injection")
	for _, value := range []any{nil, "", " \n\t", 1, true, map[string]any{"description": "text"}} {
		_, err := ApplyParamOverride(input, map[string]any{"operations": []any{map[string]any{
			"mode": "ensure_function_descriptions", "value": value,
		}}}, nil)
		require.ErrorContains(t, err, "non-empty string")
	}
	for key, value := range map[string]any{"path": "tools", "from": "source", "to": "target", "keep_origin": true} {
		operation := map[string]any{"mode": "ensure_function_descriptions", "value": "Function.", key: value}
		_, err := ApplyParamOverride(input, map[string]any{"operations": []any{operation}}, nil)
		require.ErrorContains(t, err, "only accepts value and conditions")
	}
	_, err = ApplyParamOverride([]byte(`{"tools":`), map[string]any{"operations": []any{map[string]any{
		"mode": "ensure_function_descriptions", "value": "Function.",
	}}}, nil)
	require.ErrorContains(t, err, "JSON object")
}

func TestEnsureFunctionDescriptionsHonorsConditionsAndDoesNotRepeatOtherOperations(t *testing.T) {
	input := []byte(`{"model":"gpt-test","tools":[{"type":"function","name":"lookup"}]}`)
	override := map[string]any{"operations": []any{
		map[string]any{"mode": "append", "path": "model", "value": "-once"},
		map[string]any{"mode": "ensure_function_descriptions", "value": "Function.", "conditions": []any{
			map[string]any{"path": "model", "mode": "full", "value": "gpt-test-once"},
		}},
	}}
	got, err := ApplyParamOverride(input, override, nil)
	require.NoError(t, err)
	assert.JSONEq(t, `{"model":"gpt-test-once","tools":[{"type":"function","name":"lookup","description":"Function."}]}`, string(got))
	got, err = ApplyParamOverride([]byte(`{"model":"other","tools":[{"type":"function","name":"lookup"}]}`), override, nil)
	require.NoError(t, err)
	assert.JSONEq(t, `{"model":"other-once","tools":[{"type":"function","name":"lookup"}]}`, string(got))
}

func TestExistingDescriptionOverrideLimits(t *testing.T) {
	input := []byte(`{"tools":[{"type":"function","name":"missing"},{"type":"web_search"},{"type":"function","name":"null","description":null},{"type":"function","name":"empty","description":""}]}`)
	override := map[string]any{"operations": []any{map[string]any{
		"mode": "set", "path": "tools.*.description", "value": "Function.", "keep_origin": true,
	}}}
	got, err := ApplyParamOverride(input, override, nil)
	require.NoError(t, err)
	assert.JSONEq(t, `{"tools":[{"type":"function","name":"missing","description":"Function."},{"type":"web_search","description":"Function."},{"type":"function","name":"null","description":null},{"type":"function","name":"empty","description":""}]}`, string(got), "a wildcard set adds unsupported builtin fields and keep_origin preserves null/empty")
}
