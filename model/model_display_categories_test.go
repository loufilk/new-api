package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelDisplayCategoriesValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		input   ModelDisplayCategories
		want    ModelDisplayCategories
		invalid bool
	}{
		{"omitted", nil, nil, false},
		{"automatic", ModelDisplayCategories{}, ModelDisplayCategories{}, false},
		{"canonical multimodal", ModelDisplayCategories{"video", "images", "images"}, ModelDisplayCategories{"images", "video"}, false},
		{"decision with text", ModelDisplayCategories{"decision", "text"}, ModelDisplayCategories{"text", "decision"}, false},
		{"six categories", ModelDisplayCategories{"text", "images", "video", "audio", "vectors", "decision"}, ModelDisplayCategories{"text", "images", "video", "audio", "vectors", "decision"}, false},
		{"all is not a category", ModelDisplayCategories{"all"}, nil, true},
		{"unknown", ModelDisplayCategories{"image"}, nil, true},
		{"empty value", ModelDisplayCategories{""}, nil, true},
		{"too many", ModelDisplayCategories{"text", "images", "video", "audio", "vectors", "decision", "text"}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			normalized, err := tc.input.Normalize()
			if tc.invalid {
				require.EqualError(t, err, "Invalid display categories")
				_, err = tc.input.Value()
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, normalized)
			value, err := tc.input.Value()
			require.NoError(t, err)
			var loaded ModelDisplayCategories
			require.NoError(t, loaded.Scan(value))
			assert.Equal(t, tc.want, loaded)
		})
	}
}

func TestModelDisplayCategoriesCorruptStorageFallsBackToAutomatic(t *testing.T) {
	for _, raw := range []any{nil, "", "null", "broken", `"text"`, `{}`, `["all"]`, `["text","unknown"]`, `[1]`, 7} {
		categories := ModelDisplayCategories{"images"}
		require.NoError(t, categories.Scan(raw))
		assert.Nil(t, categories, "invalid stored value %v must not survive a reused scan target", raw)
	}
	var categories ModelDisplayCategories
	require.NoError(t, categories.Scan([]byte(`["video","images","images"]`)))
	assert.Equal(t, ModelDisplayCategories{"images", "video"}, categories)
}
