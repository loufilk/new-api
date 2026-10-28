package monitor

import (
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"golang.org/x/sync/singleflight"
)

const upstreamOrigin = "https://api.bblabu.ai"
const upstreamPath = "/api/intelligence-monitor"
const maxBody = 4 << 20
const maxCacheBytes = 32 << 20
const maxStale = 10 * time.Minute

var errUnavailable = errors.New("Model monitoring is temporarily unavailable")
var resultIDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var externalLinkPattern = regexp.MustCompile(`(?i)(?:https?://|www\.)[^\s<>"']+`)
var sourceNamePattern = regexp.MustCompile(`(?i)bblabu`)

type cachedResponse struct {
	body        []byte
	contentType string
	fetched     time.Time
	retryAt     time.Time
}

// Service uses a fixed origin and fixed paths. Browser cookies, authorization,
// redirects and upstream response headers are never forwarded.
type Service struct {
	enabled    bool
	config     func() (Config, error)
	client     *http.Client
	origin     string
	now        func() time.Time
	mu         sync.Mutex
	cache      map[string]cachedResponse
	cacheBytes int
	flight     singleflight.Group
	requests   chan struct{}
}

func New(enabled bool) *Service {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxConnsPerHost = 4
	return &Service{
		enabled:  enabled,
		config:   readConfig,
		origin:   upstreamOrigin,
		now:      time.Now,
		cache:    make(map[string]cachedResponse),
		requests: make(chan struct{}, 4),
		client: &http.Client{Timeout: 12 * time.Second, Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
}

func publicText(value string) string {
	value = externalLinkPattern.ReplaceAllString(value, "[external link]")
	return sourceNamePattern.ReplaceAllString(value, "[source]")
}

func (s *Service) fetch(ctx context.Context, path, version string, ttl time.Duration) (cachedResponse, bool, error) {
	key := path + ":" + version
	s.mu.Lock()
	entry, exists := s.cache[key]
	now := s.now()
	s.mu.Unlock()
	if exists && now.Before(entry.retryAt) {
		if len(entry.body) > 0 && now.Sub(entry.fetched) <= maxStale {
			return entry, now.Sub(entry.fetched) >= ttl, nil
		}
		return cachedResponse{}, false, errUnavailable
	}
	if exists && len(entry.body) > 0 && now.Sub(entry.fetched) < ttl {
		return entry, false, nil
	}
	// A canceled visitor does not cancel the shared fetch for other visitors.
	// The HTTP timeout bounds the work; waiters can still leave immediately.
	result := s.flight.DoChan(key, func() (any, error) {
		s.mu.Lock()
		previous := s.cache[key]
		s.mu.Unlock()
		if s.now().Before(previous.retryAt) || (len(previous.body) > 0 && s.now().Sub(previous.fetched) < ttl) {
			return previous, nil
		}
		fetchContext, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		select {
		case s.requests <- struct{}{}:
			defer func() { <-s.requests }()
		case <-fetchContext.Done():
			return nil, errUnavailable
		}
		request, err := http.NewRequestWithContext(fetchContext, http.MethodGet, s.origin+upstreamPath+path, nil)
		if err != nil {
			return nil, errUnavailable
		}
		request.Header.Set("Accept", "application/json, image/svg+xml, image/png")
		response, err := s.client.Do(request)
		var body []byte
		var contentType string
		if err == nil {
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				err = errUnavailable
			} else {
				body, err = io.ReadAll(io.LimitReader(response.Body, maxBody+1))
				contentType = response.Header.Get("Content-Type")
				if len(body) > maxBody {
					err = errUnavailable
				}
				if err == nil {
					err = validateResponse(path, body, contentType)
				}
			}
		}
		next := cachedResponse{body: body, contentType: contentType, fetched: s.now()}
		if err != nil {
			next = previous
			next.retryAt = s.now().Add(15 * time.Second)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		// Bounded FIFO eviction also bounds cached failures from retired IDs.
		s.cacheBytes -= len(s.cache[key].body)
		delete(s.cache, key)
		for len(s.cache) >= 128 || s.cacheBytes+len(next.body) > maxCacheBytes {
			oldestKey := ""
			var oldest time.Time
			for candidate, cached := range s.cache {
				if oldestKey == "" || cached.fetched.Before(oldest) {
					oldestKey, oldest = candidate, cached.fetched
				}
			}
			if oldestKey == "" {
				break
			}
			s.cacheBytes -= len(s.cache[oldestKey].body)
			delete(s.cache, oldestKey)
		}
		s.cache[key] = next
		s.cacheBytes += len(next.body)
		return next, nil
	})
	select {
	case <-ctx.Done():
		return cachedResponse{}, false, ctx.Err()
	case fetched := <-result:
		if fetched.Err != nil {
			return cachedResponse{}, false, errUnavailable
		}
		entry = fetched.Val.(cachedResponse)
		if len(entry.body) == 0 || s.now().Sub(entry.fetched) > maxStale {
			return cachedResponse{}, false, errUnavailable
		}
		return entry, !entry.retryAt.IsZero() || s.now().Sub(entry.fetched) >= ttl, nil
	}
}

func validateResponse(path string, body []byte, contentType string) error {
	if strings.HasPrefix(path, "/artworks/") {
		return validateArtwork(body, contentType)
	}
	if strings.HasPrefix(path, "/thumbnails/") {
		if http.DetectContentType(body) != "image/png" {
			return errUnavailable
		}
		return nil
	}
	if !strings.HasPrefix(contentType, "application/json") {
		return errUnavailable
	}
	if path == "" {
		var payload envelope[upstreamSnapshot]
		if common.Unmarshal(body, &payload) != nil || !payload.Success || payload.Data.Enabled == nil || payload.Data.ServerTime <= 0 || len(payload.Data.Targets) > 64 || len(payload.Data.Results) > 5000 {
			return errUnavailable
		}
		return nil
	}
	var payload envelope[*Detail]
	if common.Unmarshal(body, &payload) != nil || !payload.Success || payload.Data == nil {
		return errUnavailable
	}
	return nil
}
