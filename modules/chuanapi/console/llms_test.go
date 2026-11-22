package console

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderLlmsFullIncludesPublicSectionsAndExcludesInternalPricingFields(t *testing.T) {
	bonusEligible := true
	models := publicModelsFromPricing([]publicCatalogModel{
		{
			ModelName:              "fixture-model",
			QuotaType:              0,
			ModelRatio:             1.25,
			CompletionRatio:        2,
			EnableGroup:            []string{"rate-1"},
			SupportedEndpointTypes: []constant.EndpointType{constant.EndpointTypeOpenAI},
			InputModalities:        []string{"text"},
			OutputModalities:       []string{"text"},
			Capabilities:           []string{"streaming"},
			BonusEligible:          &bonusEligible,
			BillingMode:            "tiered_expr",
			BillingExpr:            "SECRET_BILLING_EXPRESSION",
			PricingVersion:         "INTERNAL_PRICING_VERSION",
		},
	})
	require.Len(t, models, 1)

	output := renderLlmsFull(llmsPageData{
		SystemName: "Fixture API",
		Version:    "v1.2.3",
		BaseURL:    "https://fixture.example",
		Notice:     "Maintenance window",
		Announcements: []llmsAnnouncement{
			{Content: "New docs are live", PublishDate: "2026-08-31T00:00:00Z"},
		},
		FAQ: []llmsFAQ{
			{Question: "Where are the docs?", Answer: "Use /help."},
		},
		Models: models,
	})

	assert.Contains(t, output, "## Site information")
	assert.Contains(t, output, "## API endpoints")
	assert.Contains(t, output, "## Models and pricing metadata")
	assert.Contains(t, output, "| POST | `/v1/chat/completions` |")
	assert.Contains(t, output, "fixture-model")
	assert.Contains(t, output, "tiered usage pricing")
	assert.Contains(t, output, "Use /help.\n\n## API endpoints")
	assert.NotContains(t, output, "SECRET_BILLING_EXPRESSION")
	assert.NotContains(t, output, "INTERNAL_PRICING_VERSION")
}

func TestRenderLlmsIndexLinksHumanAndFullDocumentation(t *testing.T) {
	output := renderLlmsIndex(llmsPageData{
		SystemName: "Fixture API",
		Version:    "v1.2.3",
		BaseURL:    "https://fixture.example",
	})

	assert.Contains(t, output, "https://fixture.example/help")
	assert.Contains(t, output, "https://fixture.example/help/api")
	assert.Contains(t, output, "https://fixture.example/llms-full.txt")
	assert.Contains(t, output, "POST /v1/responses")
}

func TestRenderLlmsIndexKeepsDocsOnSiteAddressAndAPIOnAPIAddress(t *testing.T) {
	output := renderLlmsIndex(llmsPageData{
		SystemName: "Fixture API",
		Version:    "v1.2.3",
		BaseURL:    "https://api.example",
		DocsURL:    "https://site.example",
	})

	assert.Contains(t, output, "- Base URL: https://api.example")
	assert.Contains(t, output, "https://site.example/help")
	assert.Contains(t, output, "https://site.example/help/api")
	assert.Contains(t, output, "https://site.example/llms-full.txt")
	assert.NotContains(t, output, "https://api.example/help")
}

func TestRenderLlmsFullKeepsDocsOnSiteAddressAndAPIOnAPIAddress(t *testing.T) {
	output := renderLlmsFull(llmsPageData{
		SystemName: "Fixture API",
		Version:    "v1.2.3",
		BaseURL:    "https://api.example",
		DocsURL:    "https://site.example",
	})

	assert.Contains(t, output, "- Base URL: https://api.example")
	assert.Contains(t, output, "- Documentation center: https://site.example/help")
	assert.Contains(t, output, "- API reference: https://site.example/help/api")
	assert.Contains(t, output, "- Short AI index: https://site.example/llms.txt")
}

func TestRenderLlmsFullListsResponsesWebSocketEndpoint(t *testing.T) {
	output := renderLlmsFull(llmsPageData{SystemName: "Fixture API"})

	assert.Contains(t, output, "| GET | `/v1/responses` | Open an OpenAI Responses compatible WebSocket session. |")
}

func TestLlmsPublicBaseURLTrustsOnlyValidConfiguredAddresses(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configured string
		fallback   string
		expected   string
	}{
		{name: "unset keeps the request fallback", configured: "", fallback: "http://127.0.0.1:3000", expected: "http://127.0.0.1:3000"},
		{name: "valid configured address wins", configured: "https://site.example/base/", fallback: "http://127.0.0.1:3000", expected: "https://site.example/base"},
		{name: "invalid scheme is rejected", configured: "ftp://site.example", fallback: "http://127.0.0.1:3000", expected: ""},
		{name: "host-only value is rejected", configured: "site.example", fallback: "http://127.0.0.1:3000", expected: ""},
		{name: "query is rejected", configured: "https://site.example/?next=1", fallback: "http://127.0.0.1:3000", expected: ""},
		{name: "userinfo is rejected", configured: "https://user@site.example", fallback: "http://127.0.0.1:3000", expected: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, llmsPublicBaseURL(tc.configured, tc.fallback))
		})
	}
}

func TestLlmsRequestBaseURLValidatesUntrustedHost(t *testing.T) {
	gin.SetMode(gin.TestMode)
	requestBaseURL := func(host, forwardedProto string) string {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/llms.txt", nil)
		c.Request.Host = host
		if forwardedProto != "" {
			c.Request.Header.Set("X-Forwarded-Proto", forwardedProto)
		}
		return llmsRequestBaseURL(c)
	}

	assert.Equal(t, "https://docs.example", requestBaseURL("docs.example", "https, http"))
	assert.Equal(t, "http://docs.example:3000", requestBaseURL("docs.example:3000", ""))
	assert.Equal(t, "", requestBaseURL("bad host value", "https"))
	assert.Equal(t, "", requestBaseURL("docs.example", "ftp"))
}
