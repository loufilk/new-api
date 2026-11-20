package controller

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/common/expfmt"
	prommodel "github.com/prometheus/common/model"
)

type inferenceEndpointStatus struct {
	Status int    `json:"status"`
	Error  string `json:"error,omitempty"`
}

type inferenceModel struct {
	ID          string `json:"id"`
	Root        string `json:"root"`
	MaxModelLen *int64 `json:"max_model_len"`
}

type inferenceMetric struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
	Value  float64           `json:"value"`
}

type inferenceStatus struct {
	SampledAt  int64                              `json:"sampled_at"`
	Endpoints  map[string]inferenceEndpointStatus `json:"endpoints"`
	Version    string                             `json:"version"`
	Models     []inferenceModel                   `json:"models"`
	Metrics    []inferenceMetric                  `json:"metrics"`
	RawMetrics string                             `json:"raw_metrics"` // legacy wire field; never populated
}

func GetVLLMChannelStatus(c *gin.Context) {
	getInferenceChannelStatus(c, constant.ChannelTypeVLLM)
}

func GetSGLangChannelStatus(c *gin.Context) {
	getInferenceChannelStatus(c, constant.ChannelTypeSGLang)
}

func getInferenceChannelStatus(c *gin.Context, channelType int) {
	c.Header("Cache-Control", "no-store")
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid channel id"})
		return
	}
	channel, err := model.GetChannelById(id, true)
	if err != nil || channel == nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Channel not found"})
		return
	}
	if channel.Type != channelType {
		common.ApiError(c, errors.New("Channel type does not match the status endpoint"))
		return
	}
	status, err := fetchInferenceStatus(c.Request.Context(), channel)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, status)
}

// fetchInferenceStatus reads only fixed paths on an operator-configured channel.
// Each endpoint can fail independently; upstream errors never echo credentials.
func fetchInferenceStatus(ctx context.Context, channel *model.Channel) (*inferenceStatus, error) {
	if channel.Type != constant.ChannelTypeVLLM && channel.Type != constant.ChannelTypeSGLang {
		return nil, errors.New("This operation is only supported for vLLM or SGLang channels")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(channel.GetBaseURL()), "/")
	parsedURL, err := url.Parse(baseURL)
	if err != nil || parsedURL.Host == "" || parsedURL.User != nil || parsedURL.RawQuery != "" || parsedURL.Fragment != "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return nil, errors.New("Invalid inference server address")
	}
	key, _, keyErr := channel.GetNextEnabledKey()
	if keyErr != nil {
		return nil, errors.New("No enabled channel key")
	}
	key = strings.TrimSpace(key)
	headers, err := buildFetchModelsHeaders(channel, key)
	if err != nil {
		return nil, errors.New("Invalid channel header override")
	}
	if (key == "" || key == "EMPTY") && headers.Get("Authorization") == "Bearer "+key {
		headers.Del("Authorization")
	}
	settings := channel.GetSetting()
	client, err := service.GetHttpClientWithProxySettings(settings.Proxy, settings)
	if err != nil {
		return nil, errors.New("Invalid channel proxy")
	}
	// Do not mutate the shared client or forward channel credentials on redirects.
	statusClient := *client
	statusClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	versionPath, metricPrefix := "/version", "vllm:"
	if channel.Type == constant.ChannelTypeSGLang {
		versionPath, metricPrefix = "/server_info", "sglang:"
	}
	paths := []string{"/health", versionPath, "/v1/models", "/metrics"}
	results := make([]struct {
		inferenceEndpointStatus
		body []byte
		at   int64
	}, len(paths))
	var wg sync.WaitGroup
	for i, path := range paths {
		wg.Go(func() {
			result := &results[i]
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
			if err != nil {
				result.Error = "request_failed"
				return
			}
			req.Header = headers.Clone()
			req.Host = headers.Get("Host")
			resp, err := statusClient.Do(req)
			if err != nil {
				result.Error = "request_failed"
				return
			}
			defer resp.Body.Close()
			result.Status = resp.StatusCode
			if resp.StatusCode != http.StatusOK {
				result.Error = "http_error"
				return
			}
			const maxResponseBytes = 4 << 20
			body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
			if err != nil {
				result.Error = "request_failed"
				return
			}
			if len(body) > maxResponseBytes {
				result.Error = "response_too_large"
				return
			}
			result.body = body
			result.at = time.Now().UnixMilli()
		})
	}
	wg.Wait()
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil, context.Canceled
	}
	status := &inferenceStatus{
		SampledAt: time.Now().UnixMilli(),
		Endpoints: make(map[string]inferenceEndpointStatus, len(paths)),
		Models:    []inferenceModel{},
		Metrics:   []inferenceMetric{},
	}
	for i, path := range paths {
		result := &results[i]
		if result.Error == "" {
			switch path {
			case versionPath:
				// /server_info includes the launch configuration. Decode only the
				// version so API keys and other server settings never reach the browser.
				var version struct {
					Version string `json:"version"`
				}
				if err := common.Unmarshal(result.body, &version); err != nil || version.Version == "" {
					result.Error = "invalid_response"
				} else {
					status.Version = version.Version
				}
			case "/v1/models":
				var models struct {
					Data []inferenceModel `json:"data"`
				}
				if err := common.Unmarshal(result.body, &models); err != nil || models.Data == nil {
					result.Error = "invalid_response"
				} else {
					status.Models = models.Data
				}
			case "/metrics":
				metrics, err := parseInferenceMetrics(string(result.body), metricPrefix)
				if err != nil {
					result.Error = "invalid_response"
				} else {
					status.Metrics = metrics
					status.SampledAt = result.at
				}
			}
		}
		status.Endpoints[path] = result.inferenceEndpointStatus
	}
	return status, nil
}

func parseInferenceMetrics(raw string, prefix string) ([]inferenceMetric, error) {
	parser := expfmt.NewTextParser(prommodel.UTF8Validation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(raw))
	if err != nil {
		return nil, err
	}
	metrics := []inferenceMetric{}
	for name, family := range families {
		if !isPublicInferenceMetric(name, prefix) {
			continue
		}
		samples, err := expfmt.ExtractSamples(&expfmt.DecodeOptions{}, family)
		if err != nil {
			return nil, err
		}
		for _, sample := range samples {
			name := string(sample.Metric["__name__"])
			value := float64(sample.Value)
			if strings.HasSuffix(name, "_bucket") || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
				continue
			}
			labels := make(map[string]string, len(sample.Metric)-1)
			for label, value := range sample.Metric {
				switch label {
				case "model_name", "engine", "engine_type", "dp_rank", "tp_rank", "pp_rank", "moe_ep_rank", "sleep_state", "finished_reason":
					labels[string(label)] = string(value)
				}
			}
			metrics = append(metrics, inferenceMetric{Name: name, Labels: labels, Value: value})
		}
	}
	return metrics, nil
}

// Only time series consumed by the status panel are returned. Provider-specific
// diagnostic series and arbitrary labels are not part of its response contract.
func isPublicInferenceMetric(name, prefix string) bool {
	if name == "process_start_time_seconds" {
		return true
	}
	local, ok := strings.CutPrefix(name, prefix)
	if !ok {
		return false
	}
	// Histogram totals may be exposed as their own families. Created timestamps
	// are used alongside process start time to recognize counter restarts.
	for _, suffix := range []string{"_sum", "_count", "_created"} {
		local = strings.TrimSuffix(local, suffix)
	}
	switch local {
	case "prompt_tokens_total", "generation_tokens_total", "time_to_first_token_seconds", "e2e_request_latency_seconds":
		return true
	}
	switch prefix {
	case "vllm:":
		switch local {
		case "num_requests_running", "num_requests_waiting", "kv_cache_usage_perc", "gpu_cache_usage_perc",
			"engine_sleep_state", "request_success_total", "prefix_cache_hits_total", "prefix_cache_queries_total",
			"spec_decode_num_accepted_tokens_total", "spec_decode_num_draft_tokens_total",
			"request_queue_time_seconds", "request_time_per_output_token_seconds":
			return true
		}
	case "sglang:":
		switch local {
		case "num_running_reqs", "num_queue_reqs", "token_usage", "gen_throughput", "num_requests_total",
			"cached_tokens_total", "queue_time_seconds", "inter_token_latency_seconds", "time_per_output_token_seconds",
			"cache_hit_rate", "spec_accept_rate":
			return true
		}
	}
	return false
}
