package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestPartnerApplicationBodyRejectsWrongMediaType(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/partner-applications", strings.NewReader(`{}`))
	context.Request.Header.Set("Content-Type", "text/plain")

	var input model.PartnerApplicationInput
	assert.False(t, partnerApplicationBody(context, &input))
	assert.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestPartnerApplicationBodyRejectsOversizedAndUnknownFields(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"oversized JSON", strings.Repeat(" ", partnerApplicationBodyLimit+1)},
		{"unknown JSON field", `{"unexpected":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodPost, "/api/partner-applications", strings.NewReader(tc.body))
			context.Request.Header.Set("Content-Type", "application/json")

			var input model.PartnerApplicationInput
			assert.False(t, partnerApplicationBody(context, &input))
			if tc.name == "oversized JSON" {
				assert.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
			} else {
				assert.Equal(t, http.StatusBadRequest, recorder.Code)
			}
		})
	}
}
