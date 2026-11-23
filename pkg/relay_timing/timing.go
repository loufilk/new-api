// Package relay_timing provides sampled, payload-free timing for HTTP relays.
package relay_timing

import (
	"math/rand/v2"
	"net/http"
	"net/http/httptrace"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"

	"github.com/gin-gonic/gin"
)

const (
	envKey            = "RELAY_TIMING_SAMPLE_PERMILLE"
	contextKey        = "relay_timing_sample"
	maxUpstreamTraces = 8
)

var samplePermille = sync.OnceValue(func() uint64 {
	return parseSamplePermille(os.Getenv(envKey))
})

func parseSamplePermille(raw string) uint64 {
	n, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 16)
	if err != nil || n > 1000 {
		return 0
	}
	return n
}

// Sample holds only durations, numeric statuses and transport flags. It never
// retains the URL, headers, user identifiers, upstream errors or payloads.
type Sample struct {
	mu                   sync.Mutex
	ingress, entered     time.Time
	initialRouteSelected time.Time
	billingMS            *int64
	upstreamAttempts     int
	upstreams            []UpstreamReport
}

type Report struct {
	IngressToRelayMS              *int64           `json:"ingress_to_relay_ms,omitempty"`
	IngressToRouteSelectionMS     *int64           `json:"ingress_to_route_selection_ms,omitempty"`
	BillingPrepareMS              *int64           `json:"billing_prepare_ms,omitempty"`
	RelayHandlerMS                *int64           `json:"relay_handler_ms,omitempty"`
	IngressToFirstUpstreamEventMS *int64           `json:"ingress_to_first_upstream_event_ms,omitempty"`
	IngressToFirstUsableOutputMS  *int64           `json:"ingress_to_first_usable_output_ms,omitempty"`
	Status                        int              `json:"status"`
	RelayAPIError                 bool             `json:"relay_api_error"`
	UpstreamAttempts              int              `json:"upstream_attempts"`
	Upstreams                     []UpstreamReport `json:"upstreams,omitempty"`
}

type UpstreamReport struct {
	RelayToDispatchMS  *int64 `json:"relay_to_dispatch_ms,omitempty"`
	GetConnectionMS    *int64 `json:"get_connection_ms,omitempty"`
	ConnectionReused   *bool  `json:"connection_reused,omitempty"`
	WrittenToHeadersMS *int64 `json:"written_to_first_header_byte_ms,omitempty"`
	DispatchToResultMS *int64 `json:"dispatch_to_result_ms,omitempty"`
	ProtocolMajor      int    `json:"protocol_major,omitempty"`
	Status             int    `json:"status,omitempty"`
}

func Begin(c *gin.Context) *Sample {
	rate := samplePermille()
	if rate == 0 || (rate < 1000 && rand.Uint64N(1000) >= rate) {
		return nil
	}
	s := &Sample{
		entered:              time.Now(),
		ingress:              common.GetContextKeyTime(c, constant.ContextKeyGatewayIngressTime),
		initialRouteSelected: common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime),
	}
	c.Set(contextKey, s)
	return s
}

func (s *Sample) RecordBillingPreparation(start time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.billingMS = elapsedMS(start, time.Now())
}

// Finish is read after the relay/stream handler returns. The downstream
// marker is optional: native Chat records it after a successful flush of
// content, reasoning, a refusal, audio or a tool delta.
func (s *Sample) Finish(status int, relayError bool, firstUpstreamEvent, firstUsableOutput time.Time) Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Report{
		IngressToRelayMS:              elapsedMS(s.ingress, s.entered),
		IngressToRouteSelectionMS:     elapsedMS(s.ingress, s.initialRouteSelected),
		BillingPrepareMS:              s.billingMS,
		RelayHandlerMS:                elapsedMS(s.entered, time.Now()),
		IngressToFirstUpstreamEventMS: elapsedMS(s.ingress, firstUpstreamEvent),
		IngressToFirstUsableOutputMS:  elapsedMS(s.ingress, firstUsableOutput),
		Status:                        status,
		RelayAPIError:                 relayError,
		UpstreamAttempts:              s.upstreamAttempts,
		Upstreams:                     append([]UpstreamReport(nil), s.upstreams...),
	}
}

// TraceUpstream attaches observers without changing dispatch, replays,
// cancellation or the shared client's connection pool. It returns the original
// request and no completion function when sampling is disabled.
func TraceUpstream(c *gin.Context, req *http.Request) (*http.Request, func(*http.Response)) {
	if samplePermille() == 0 {
		return req, nil
	}
	value, _ := c.Get(contextKey)
	sample, _ := value.(*Sample)
	if sample == nil {
		return req, nil
	}
	return sample.traceUpstream(req)
}

func (s *Sample) traceUpstream(req *http.Request) (*http.Request, func(*http.Response)) {
	s.mu.Lock()
	s.upstreamAttempts++
	index := s.upstreamAttempts - 1
	if index >= maxUpstreamTraces {
		s.mu.Unlock()
		return req, nil
	}
	s.upstreams = append(s.upstreams, UpstreamReport{})
	s.mu.Unlock()

	began := time.Now()
	var mu sync.Mutex // httptrace hooks can run on transport goroutines.
	var getConn, wrote, firstHeader time.Time
	var connectionMS *int64
	var connectionReused *bool
	trace := &httptrace.ClientTrace{
		GetConn: func(string) {
			mu.Lock()
			defer mu.Unlock()
			getConn = time.Now()
			// Transport retries can acquire another connection on the same
			// dispatch. Keep the latest acquisition and successful write.
			connectionMS, connectionReused = nil, nil
			wrote, firstHeader = time.Time{}, time.Time{}
		},
		GotConn: func(info httptrace.GotConnInfo) {
			mu.Lock()
			defer mu.Unlock()
			connectionMS = elapsedMS(getConn, time.Now())
			connectionReused = &info.Reused
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			wrote = time.Now()
		},
		GotFirstResponseByte: func() {
			mu.Lock()
			defer mu.Unlock()
			if firstHeader.IsZero() {
				firstHeader = time.Now()
			}
		},
	}
	traced := req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	return traced, func(resp *http.Response) {
		mu.Lock()
		result := UpstreamReport{
			RelayToDispatchMS:  elapsedMS(s.entered, began),
			GetConnectionMS:    connectionMS,
			ConnectionReused:   connectionReused,
			WrittenToHeadersMS: elapsedMS(wrote, firstHeader),
			DispatchToResultMS: elapsedMS(began, time.Now()),
		}
		mu.Unlock()
		if resp != nil {
			result.ProtocolMajor = resp.ProtoMajor
			result.Status = resp.StatusCode
		}
		s.mu.Lock()
		s.upstreams[index] = result
		s.mu.Unlock()
	}
}

func elapsedMS(start, end time.Time) *int64 {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return nil
	}
	result := end.Sub(start).Milliseconds()
	return &result
}
