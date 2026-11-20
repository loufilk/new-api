package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestInvalidPricingOptionPreservesActiveRatios(t *testing.T) {
	tests := []struct {
		key     string
		current func() string
		set     func(string) error
	}{
		{"ImageRatio", ratio_setting.ImageRatio2JSONString, ratio_setting.UpdateImageRatioByJSONString},
		{"AudioRatio", ratio_setting.AudioRatio2JSONString, ratio_setting.UpdateAudioRatioByJSONString},
		{"AudioCompletionRatio", ratio_setting.AudioCompletionRatio2JSONString, ratio_setting.UpdateAudioCompletionRatioByJSONString},
		{"CreateCacheRatio", ratio_setting.CreateCacheRatio2JSONString, ratio_setting.UpdateCreateCacheRatioByJSONString},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			original := tt.current()
			t.Cleanup(func() { require.NoError(t, tt.set(original)) })
			require.NoError(t, tt.set(`{"example-model":1.25}`))

			body, err := common.Marshal(map[string]string{"key": tt.key, "value": `{"example-model":"invalid"}`})
			require.NoError(t, err)
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodPut, "/api/option/", bytes.NewReader(body))
			UpdateOption(context)

			require.Contains(t, recorder.Body.String(), `"success":false`)
			require.JSONEq(t, `{"example-model":1.25}`, tt.current())
		})
	}
}
