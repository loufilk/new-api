package console

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/console_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
)

type llmsEndpoint struct {
	Method      string
	Path        string
	Description string
}

type llmsAnnouncement struct {
	Content     string
	PublishDate string
	Extra       string
}

type llmsFAQ struct {
	Question string
	Answer   string
}

type llmsModel struct {
	Name          string
	Billing       string
	Groups        []string
	Endpoints     []string
	InputModes    []string
	OutputModes   []string
	Capabilities  []string
	BonusEligible string
}

type llmsPageData struct {
	SystemName string
	Version    string
	BaseURL    string
	// DocsURL is the browser-facing documentation origin. It falls back to
	// BaseURL when unset so a single-domain deployment keeps one address.
	DocsURL       string
	Notice        string
	Announcements []llmsAnnouncement
	FAQ           []llmsFAQ
	Models        []llmsModel
}

var llmsEndpointCatalog = []llmsEndpoint{
	{Method: http.MethodGet, Path: "/v1/models", Description: "List models available to the current API token group."},
	{Method: http.MethodGet, Path: "/v1/models/{model}", Description: "Retrieve one model by its exact model id."},
	{Method: http.MethodPost, Path: "/v1/chat/completions", Description: "OpenAI-compatible chat completions."},
	{Method: http.MethodPost, Path: "/v1/responses", Description: "OpenAI Responses API compatible generation."},
	{Method: http.MethodPost, Path: "/v1/responses/compact", Description: "Compact a Responses conversation context."},
	{Method: http.MethodPost, Path: "/v1/messages", Description: "Anthropic Messages compatible generation."},
	{Method: http.MethodGet, Path: "/v1/responses", Description: "Open an OpenAI Responses compatible WebSocket session."},
	{Method: http.MethodPost, Path: "/v1/completions", Description: "Legacy OpenAI text completions."},
	{Method: http.MethodPost, Path: "/v1/embeddings", Description: "Create text embeddings."},
	{Method: http.MethodPost, Path: "/v1/rerank", Description: "Rerank documents against a query."},
	{Method: http.MethodPost, Path: "/v1/moderations", Description: "Evaluate input with a moderation model."},
	{Method: http.MethodPost, Path: "/v1/audio/transcriptions", Description: "Transcribe an uploaded audio file."},
	{Method: http.MethodPost, Path: "/v1/audio/translations", Description: "Translate and transcribe an uploaded audio file."},
	{Method: http.MethodPost, Path: "/v1/audio/speech", Description: "Synthesize speech audio from text."},
	{Method: http.MethodGet, Path: "/v1/realtime", Description: "Open an OpenAI Realtime compatible WebSocket session."},
	{Method: http.MethodPost, Path: "/v1/images/generations", Description: "Generate images from a prompt."},
	{Method: http.MethodPost, Path: "/v1/images/edits", Description: "Edit an uploaded image from a prompt."},
	{Method: http.MethodPost, Path: "/v1beta/models/{model}:generateContent", Description: "Gemini native generateContent compatible generation."},
}

func GetLlmsIndex(c *gin.Context) {
	data := buildLlmsPageData(c)
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(renderLlmsIndex(data)))
}

func GetLlmsFull(c *gin.Context) {
	data := buildLlmsPageData(c)
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(renderLlmsFull(data)))
}

// normalizeLlmsBaseURL validates an absolute public origin. Only http(s)
// URLs with a host are accepted; userinfo, queries and fragments are rejected.
// The returned value never has a trailing slash.
func normalizeLlmsBaseURL(raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil {
		return "", false
	}
	if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return "", false
	}
	if parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return "", false
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	return strings.TrimRight(parsed.String(), "/"), true
}

// llmsRequestBaseURL derives the legacy fallback origin from the request. The
// Host header is untrusted input, so it is only used after structured URL
// validation; a malformed value yields an empty base instead of a bogus
// canonical address.
func llmsRequestBaseURL(c *gin.Context) string {
	proto := strings.TrimSpace(strings.Split(c.GetHeader("X-Forwarded-Proto"), ",")[0])
	if proto == "" {
		if c.Request.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	normalized, ok := normalizeLlmsBaseURL(proto + "://" + strings.TrimSpace(c.Request.Host))
	if !ok {
		return ""
	}
	return normalized
}

// llmsPublicBaseURL resolves one public address. An address configured by an
// administrator always wins; a configured but invalid value yields an empty
// canonical instead of falling back to the request-controlled host.
func llmsPublicBaseURL(configured, fallback string) string {
	if strings.TrimSpace(configured) == "" {
		return fallback
	}
	normalized, ok := normalizeLlmsBaseURL(configured)
	if !ok {
		return ""
	}
	return normalized
}

func buildLlmsPageData(c *gin.Context) llmsPageData {
	fallbackBaseURL := llmsRequestBaseURL(c)
	// Human documentation lives on the browser-facing site address while relay
	// samples must point at the public API address; split deployments configure
	// them separately and never let the request Host define the canonical URL.
	apiBaseURL := llmsPublicBaseURL(system_setting.TaskPublicAddress, fallbackBaseURL)
	docsURL := llmsPublicBaseURL(system_setting.ServerAddress, fallbackBaseURL)

	common.OptionMapRWMutex.RLock()
	notice := common.OptionMap["Notice"]
	common.OptionMapRWMutex.RUnlock()

	consoleSetting := console_setting.GetConsoleSetting()
	announcements := make([]llmsAnnouncement, 0)
	if consoleSetting.AnnouncementsEnabled {
		for _, item := range console_setting.GetAnnouncements() {
			content, _ := item["content"].(string)
			publishDate, _ := item["publishDate"].(string)
			extra, _ := item["extra"].(string)
			if strings.TrimSpace(content) == "" {
				continue
			}
			announcements = append(announcements, llmsAnnouncement{
				Content:     content,
				PublishDate: publishDate,
				Extra:       extra,
			})
		}
	}

	faq := make([]llmsFAQ, 0)
	if consoleSetting.FAQEnabled {
		for _, item := range console_setting.GetFAQ() {
			question, _ := item["question"].(string)
			answer, _ := item["answer"].(string)
			if strings.TrimSpace(question) == "" || strings.TrimSpace(answer) == "" {
				continue
			}
			faq = append(faq, llmsFAQ{Question: question, Answer: answer})
		}
	}

	catalog := buildPublicCatalog()

	return llmsPageData{
		SystemName:    common.SystemName,
		Version:       common.Version,
		BaseURL:       apiBaseURL,
		DocsURL:       docsURL,
		Notice:        notice,
		Announcements: announcements,
		FAQ:           faq,
		Models:        publicModelsFromPricing(catalog.Models),
	}
}

func publicModelsFromPricing(pricing []publicCatalogModel) []llmsModel {
	models := make([]llmsModel, 0, len(pricing))
	for _, item := range pricing {
		billing := "token ratios: input " + strconv.FormatFloat(item.ModelRatio, 'g', -1, 64) +
			", output " + strconv.FormatFloat(item.CompletionRatio, 'g', -1, 64)
		if item.QuotaType == 1 {
			billing = "per-request price factor: " + strconv.FormatFloat(item.ModelPrice, 'g', -1, 64)
		} else if item.BillingMode == "tiered_expr" {
			billing = "tiered usage pricing"
		}

		endpoints := make([]string, 0, len(item.SupportedEndpointTypes))
		for _, endpoint := range item.SupportedEndpointTypes {
			endpoints = append(endpoints, string(endpoint))
		}
		sort.Strings(endpoints)

		groups := append([]string(nil), item.EnableGroup...)
		sort.Strings(groups)
		bonusEligible := "not configured"
		if item.BonusEligible != nil {
			bonusEligible = strconv.FormatBool(*item.BonusEligible)
		}

		models = append(models, llmsModel{
			Name:          item.ModelName,
			Billing:       billing,
			Groups:        groups,
			Endpoints:     endpoints,
			InputModes:    append([]string(nil), item.InputModalities...),
			OutputModes:   append([]string(nil), item.OutputModalities...),
			Capabilities:  append([]string(nil), item.Capabilities...),
			BonusEligible: bonusEligible,
		})
	}
	sort.Slice(models, func(i, j int) bool {
		return strings.ToLower(models[i].Name) < strings.ToLower(models[j].Name)
	})
	return models
}

// llmsDocsURL returns the browser-facing documentation origin. A single-domain
// deployment that only fills BaseURL keeps using it for documentation links.
func llmsDocsURL(data llmsPageData) string {
	if docsURL := strings.TrimRight(strings.TrimSpace(data.DocsURL), "/"); docsURL != "" {
		return docsURL
	}
	return strings.TrimRight(strings.TrimSpace(data.BaseURL), "/")
}

func renderLlmsIndex(data llmsPageData) string {
	var builder strings.Builder
	docsURL := llmsDocsURL(data)
	fmt.Fprintf(&builder, "# %s\n\n", plainText(data.SystemName))
	builder.WriteString("Public AI API gateway documentation index.\n\n")
	fmt.Fprintf(&builder, "- Version: %s\n", plainText(data.Version))
	fmt.Fprintf(&builder, "- Base URL: %s\n", plainText(data.BaseURL))
	fmt.Fprintf(&builder, "- Human documentation: %s/help\n", plainText(docsURL))
	fmt.Fprintf(&builder, "- API reference: %s/help/api\n", plainText(docsURL))
	fmt.Fprintf(&builder, "- Full AI context: %s/llms-full.txt\n\n", plainText(docsURL))
	builder.WriteString("## Core endpoints\n\n")
	for _, endpoint := range llmsEndpointCatalog[:6] {
		fmt.Fprintf(&builder, "- `%s %s` — %s\n", endpoint.Method, endpoint.Path, endpoint.Description)
	}
	builder.WriteString("\nAuthentication normally uses `Authorization: Bearer API_TOKEN`; Anthropic and Gemini native routes also accept their native API-key headers.\n")
	return builder.String()
}

func renderLlmsFull(data llmsPageData) string {
	var builder strings.Builder
	docsURL := llmsDocsURL(data)
	fmt.Fprintf(&builder, "# %s — Full AI context\n\n", plainText(data.SystemName))
	builder.WriteString("This document describes the public documentation, runtime notices, API routes, FAQ, and public model pricing metadata.\n\n")

	builder.WriteString("## Site information\n\n")
	fmt.Fprintf(&builder, "- System: %s\n", plainText(data.SystemName))
	fmt.Fprintf(&builder, "- Version: %s\n", plainText(data.Version))
	fmt.Fprintf(&builder, "- Base URL: %s\n", plainText(data.BaseURL))
	fmt.Fprintf(&builder, "- Documentation center: %s/help\n", plainText(docsURL))
	fmt.Fprintf(&builder, "- API reference: %s/help/api\n", plainText(docsURL))
	fmt.Fprintf(&builder, "- Short AI index: %s/llms.txt\n", plainText(docsURL))

	builder.WriteString("\n## System notice\n\n")
	if strings.TrimSpace(data.Notice) == "" {
		builder.WriteString("No active system notice.\n")
	} else {
		builder.WriteString(plainText(data.Notice) + "\n")
	}

	builder.WriteString("\n## Announcements\n\n")
	if len(data.Announcements) == 0 {
		builder.WriteString("No published announcements.\n")
	} else {
		for _, announcement := range data.Announcements {
			date := plainText(announcement.PublishDate)
			if date == "" {
				date = "date not specified"
			}
			fmt.Fprintf(&builder, "- %s — %s", date, plainText(announcement.Content))
			if extra := plainText(announcement.Extra); extra != "" {
				fmt.Fprintf(&builder, " (%s)", extra)
			}
			builder.WriteString("\n")
		}
	}

	builder.WriteString("\n## FAQ\n\n")
	if len(data.FAQ) == 0 {
		builder.WriteString("No runtime FAQ entries are configured. See the documentation center for static integration guidance.\n\n")
	} else {
		for _, item := range data.FAQ {
			fmt.Fprintf(&builder, "### %s\n\n%s\n\n", plainText(item.Question), plainText(item.Answer))
		}
	}

	builder.WriteString("## API endpoints\n\n")
	builder.WriteString("All relay endpoints require an API token. Actual availability depends on the public model catalog and token group.\n\n")
	builder.WriteString("| Method | Path | Description |\n")
	builder.WriteString("| --- | --- | --- |\n")
	for _, endpoint := range llmsEndpointCatalog {
		fmt.Fprintf(
			&builder,
			"| %s | `%s` | %s |\n",
			endpoint.Method,
			markdownCell(endpoint.Path),
			markdownCell(endpoint.Description),
		)
	}

	builder.WriteString("\n## Models and pricing metadata\n\n")
	builder.WriteString("The table uses the same public model catalogue source as `/public-api/pricing`. Price factors are runtime metadata, not a promise that every model is available to every token group.\n\n")
	if len(data.Models) == 0 {
		builder.WriteString("No public models are currently listed.\n")
	} else {
		builder.WriteString("| Model | Billing | Groups | Endpoints | Input | Output | Capabilities | Bonus eligible |\n")
		builder.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- |\n")
		for _, item := range data.Models {
			fmt.Fprintf(
				&builder,
				"| `%s` | %s | %s | %s | %s | %s | %s | %s |\n",
				markdownCell(item.Name),
				markdownCell(item.Billing),
				markdownCell(strings.Join(item.Groups, ", ")),
				markdownCell(strings.Join(item.Endpoints, ", ")),
				markdownCell(strings.Join(item.InputModes, ", ")),
				markdownCell(strings.Join(item.OutputModes, ", ")),
				markdownCell(strings.Join(item.Capabilities, ", ")),
				markdownCell(item.BonusEligible),
			)
		}
	}

	return builder.String()
}

func plainText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func markdownCell(value string) string {
	return strings.ReplaceAll(plainText(value), "|", "\\|")
}
