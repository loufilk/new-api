package cline

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
)

const (
	RecommendedModelsPath       = "/ai/cline/recommended-models"
	recommendedModelsMaxBytes   = 2 << 20
	recommendedModelsTimeout    = 15 * time.Second
	recommendedModelsRedirects  = 3
	recommendedModelsUserAgent  = "new-api-cline-discovery"
	recommendedModelsAcceptType = "application/json"
)

type recommendedModelEntry struct {
	ID string `json:"id"`
}

type recommendedModelsResponse struct {
	Recommended []recommendedModelEntry `json:"recommended"`
	Free        []recommendedModelEntry `json:"free"`
	ClinePass   []recommendedModelEntry `json:"clinePass"`
	ClineCloud  []recommendedModelEntry `json:"clineCloud"`
}

// FetchRecommendedModelIDs reads Cline's public model catalog. The catalog is
// unauthenticated, so no channel credential is ever attached and redirects are
// restricted to the same origin. Free models lead the merged order, followed by
// the remaining published groups, with exact-match deduplication and the
// provider prefixes preserved verbatim.
func FetchRecommendedModelIDs(baseURL, proxy string) ([]string, error) {
	endpoint, err := RecommendedModelsURL(baseURL)
	if err != nil {
		return nil, err
	}
	client, err := service.NewSSRFProtectedHTTPClientWithProxy(proxy)
	if err != nil {
		return nil, err
	}
	fetchClient := *client
	fetchClient.Timeout = recommendedModelsTimeout
	fetchClient.CheckRedirect = limitRecommendedModelsRedirects

	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", recommendedModelsAcceptType)
	request.Header.Set("User-Agent", recommendedModelsUserAgent)
	response, err := fetchClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cline recommended-models request failed with status code %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, recommendedModelsMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > recommendedModelsMaxBytes {
		return nil, fmt.Errorf("cline recommended-models response exceeds %d bytes", recommendedModelsMaxBytes)
	}
	var payload recommendedModelsResponse
	if err := common.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("invalid cline recommended-models response: %w", err)
	}

	groups := [][]recommendedModelEntry{
		payload.Free,
		payload.Recommended,
		payload.ClinePass,
		payload.ClineCloud,
	}
	models := make([]string, 0, len(payload.Recommended)+len(payload.Free)+len(payload.ClinePass)+len(payload.ClineCloud))
	seen := make(map[string]struct{}, cap(models))
	for _, group := range groups {
		for _, entry := range group {
			id := strings.TrimSpace(entry.ID)
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			models = append(models, id)
		}
	}
	if len(models) == 0 {
		return nil, errors.New("cline recommended-models response contains no model IDs")
	}
	return models, nil
}

func RecommendedModelsURL(baseURL string) (string, error) {
	base := strings.TrimSpace(baseURL)
	if base == "" {
		base = DefaultBaseURL
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("invalid cline base URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("cline base URL must use http or https")
	}
	if parsed.Host == "" {
		return "", errors.New("cline base URL must include a host")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + RecommendedModelsPath
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func limitRecommendedModelsRedirects(request *http.Request, via []*http.Request) error {
	if err := service.ValidateSSRFProtectedFetchURL(request.URL.String()); err != nil {
		return fmt.Errorf("cline recommended-models redirect target blocked: %w", err)
	}
	if len(via) >= recommendedModelsRedirects {
		return errors.New("too many cline recommended-models redirects")
	}
	if len(via) == 0 {
		return nil
	}
	origin := via[0].URL
	if !strings.EqualFold(request.URL.Host, origin.Host) {
		return errors.New("cline recommended-models redirect to a different host is not allowed")
	}
	if origin.Scheme == "https" && request.URL.Scheme != "https" {
		return errors.New("cline recommended-models redirect must stay on https")
	}
	return nil
}
