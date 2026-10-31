package helper

import (
	"maps"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func ResolveIncomingBillingExprRequestInput(c *gin.Context, info *relaycommon.RelayInfo) (billingexpr.RequestInput, error) {
	requestTime := time.Now()
	if info != nil && !info.StartTime.IsZero() {
		requestTime = info.StartTime
	}
	if info != nil && info.BillingRequestInput != nil {
		input := cloneRequestInput(*info.BillingRequestInput)
		if input.Timestamp <= 0 {
			input.Timestamp = requestTime.Unix()
		}
		merged := requestHeadersFromContext(c)
		maps.Copy(merged, cloneStringMap(info.RequestHeaders))
		maps.Copy(merged, input.Headers)
		input.Headers = merged
		return input, nil
	}

	input := billingexpr.RequestInput{Headers: requestHeadersFromContext(c), Timestamp: requestTime.Unix()}
	if info != nil {
		maps.Copy(input.Headers, cloneStringMap(info.RequestHeaders))
	}

	bodyBytes, err := readIncomingBillingExprBody(c)
	if err != nil {
		return billingexpr.RequestInput{}, err
	}
	input.Body = bodyBytes
	return input, nil
}

// ResolveImageBillingRequestInput freezes only the validated scalar image
// parameters needed by pricing. Image files, prompts and base64 payloads are
// deliberately excluded, including for multipart edits.
func ResolveImageBillingRequestInput(c *gin.Context, info *relaycommon.RelayInfo, input billingexpr.RequestInput) (billingexpr.RequestInput, error) {
	request, ok := info.Request.(*dto.ImageRequest)
	if !ok {
		return input, nil
	}
	return ResolveImageBillingInputForRequest(c, request, input)
}

// ResolveImageBillingInputForRequest is shared by the ingress estimate and the
// final outbound request after channel mapping/overrides. Only pricing scalars
// are retained; a retry replaces them without touching the replayable body.
func ResolveImageBillingInputForRequest(c *gin.Context, request *dto.ImageRequest, input billingexpr.RequestInput) (billingexpr.RequestInput, error) {
	count, err := request.ImageCount(false)
	if err != nil {
		return input, err
	}
	references, err := ImageRequestReferenceCount(c, request)
	if err != nil {
		return input, err
	}
	// param("model") is the administrator's client-facing billing identity,
	// not the channel's mapped routing name. Preserve its existing contract
	// while exposing the actual route separately for opt-in pricing rules.
	billingModel, _ := input.Params["model"].(string)
	if billingModel == "" {
		billingModel = gjson.GetBytes(input.Body, "model").String()
	}
	if billingModel == "" {
		billingModel = request.Model
	}
	body := map[string]any{"model": billingModel, "upstream_model": request.Model, "n": count, "size": request.Size, "quality": request.Quality, "reference_count": references}
	if request.Resolution != nil {
		body["resolution"] = *request.Resolution
	}
	if request.AspectRatio != nil {
		body["aspect_ratio"] = *request.AspectRatio
	}
	if (request.Model == "grok-imagine-image" || request.Model == "grok-imagine-image-2.0" || request.Model == "grok-imagine-image-quality" || request.Model == "grok-imagine-image-pro") && request.Resolution == nil {
		body["resolution"] = "1k"
	}
	if request.Model == "grok-imagine-image-2.0" && (request.Quality == "" || request.Quality == "auto") {
		body["quality"] = "low"
		if c != nil && c.Request != nil && strings.HasSuffix(c.Request.URL.Path, "/edits") {
			body["quality"] = "medium"
		}
	}
	if request.BillingParameters != nil {
		body["parameters"] = request.BillingParameters
	}
	encoded, err := common.Marshal(body)
	if err != nil {
		return input, err
	}
	input.Params = maps.Clone(input.Params)
	for _, name := range []string{"model", "upstream_model", "n", "size", "quality", "resolution", "aspect_ratio", "reference_count", "parameters"} {
		delete(input.Params, name)
	}
	input.Body = encoded
	input.ImageCount = &count
	return input, nil
}

func BuildBillingExprRequestInputFromRequest(request dto.Request, headers map[string]string) (billingexpr.RequestInput, error) {
	input := billingexpr.RequestInput{
		Headers: cloneStringMap(headers),
	}
	if request == nil {
		return input, nil
	}

	bodyBytes, err := common.Marshal(request)
	if err != nil {
		return billingexpr.RequestInput{}, err
	}
	input.Body = bodyBytes
	return input, nil
}

func readIncomingBillingExprBody(c *gin.Context) ([]byte, error) {
	if c == nil || c.Request == nil || !isJSONContentType(c.Request.Header.Get("Content-Type")) {
		return nil, nil
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, err
	}
	return storage.Bytes()
}

func cloneRequestInput(src billingexpr.RequestInput) billingexpr.RequestInput {
	input := billingexpr.RequestInput{
		Headers:   cloneStringMap(src.Headers),
		Timestamp: src.Timestamp,
	}
	if len(src.Params) > 0 {
		input.Params = maps.Clone(src.Params)
	}
	if src.ImageCount != nil {
		count := *src.ImageCount
		input.ImageCount = &count
	}
	if len(src.Body) > 0 {
		input.Body = append([]byte(nil), src.Body...)
	}
	if len(src.Usage) > 0 {
		input.Usage = maps.Clone(src.Usage)
	}
	return input
}

func isJSONContentType(contentType string) bool {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	return strings.HasPrefix(contentType, "application/json")
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return map[string]string{}
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		if strings.TrimSpace(key) == "" {
			continue
		}
		dst[key] = value
	}
	return dst
}

func requestHeadersFromContext(c *gin.Context) map[string]string {
	headers := map[string]string{}
	if c == nil || c.Request == nil {
		return headers
	}
	for key, values := range c.Request.Header {
		if strings.TrimSpace(key) == "" || len(values) == 0 {
			continue
		}
		headers[key] = values[0]
	}
	return headers
}
