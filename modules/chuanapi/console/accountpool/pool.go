// Package accountpool is a read-only, explicitly configured Sub2 projection.
// The channel's relay key is NEVER used to probe an administration endpoint.
package accountpool

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"golang.org/x/sync/singleflight"
)

const cacheTTL = time.Minute
const maxStale = 5 * time.Minute
const maxAccounts = 5000

type Binding struct {
	ChannelID int `json:"channel_id"`
	// Root-owned environment configuration pins the management credential to
	// one origin. A channel.write administrator cannot redirect this secret by
	// editing the ordinary relay channel's BaseURL.
	Origin string `json:"origin"`
	// Keys are verified Sub2 group IDs; values are existing NewAPI group names.
	// No inferred cross-service group identity or implicit publish-all behavior.
	GroupMap map[string]string `json:"group_map"`
}
type Group struct {
	Name  string  `json:"name"`
	Ratio float64 `json:"ratio"`
}
type Item struct {
	AnonymousID      string   `json:"anonymous_id"`
	Provider         string   `json:"provider"`
	AuthType         string   `json:"auth_type"`
	Plan             string   `json:"plan"`
	Status           string   `json:"status"`
	Concurrency      *int     `json:"concurrency"`
	ConcurrencyLimit *int     `json:"concurrency_limit"`
	Groups           []Group  `json:"groups"`
	Usage5hPercent   *float64 `json:"usage_5h_percent"`
	Usage7dPercent   *float64 `json:"usage_7d_percent"`
	LastUsedAt       *int64   `json:"last_used_at"`
}
type GroupSummary struct {
	Group
	Available        int  `json:"available"`
	Total            int  `json:"total"`
	Concurrency      *int `json:"concurrency"`
	ConcurrencyLimit *int `json:"concurrency_limit"`
}
type Snapshot struct {
	Configured        bool           `json:"configured"`
	State             string         `json:"state"`
	UpdatedAt         int64          `json:"updated_at"`
	CacheTTLSeconds   int            `json:"cache_ttl_seconds"`
	StaleAfterSeconds int            `json:"stale_after_seconds"`
	Items             []Item         `json:"items"`
	Total             int            `json:"total"`
	Page              int            `json:"page"`
	PageSize          int            `json:"page_size"`
	Groups            []GroupSummary `json:"groups"`
}
type NodeStatus struct {
	ChannelID int    `json:"channel_id"`
	State     string `json:"state"`
	Count     int    `json:"count"`
	ErrorCode string `json:"error_code,omitempty"`
}
type cached struct {
	items      []Item
	nodes      []NodeStatus
	fetched    time.Time
	retryAt    time.Time
	configured bool
}
type Service struct {
	bindings []Binding
	invalid  bool
	client   *http.Client
	mu       sync.Mutex
	cache    cached
	flight   singleflight.Group
	now      func() time.Time
	getenv   func(string) string
	channel  func(int) (*model.Channel, error)
	enabled  func(context.Context) (bool, error)
}

func New() *Service {
	s := &Service{client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, now: time.Now, getenv: os.Getenv, channel: func(id int) (*model.Channel, error) { return model.GetChannelById(id, false) }}
	s.enabled = model.AccountPoolEnabled
	raw := os.Getenv("SUB2_POOL_CHANNELS")
	if raw != "" && (len(raw) > 8192 || common.UnmarshalJsonStr(raw, &s.bindings) != nil || len(s.bindings) > 8) {
		s.invalid = true
	}
	for _, b := range s.bindings {
		if _, err := poolOrigin(b.Origin); err != nil {
			s.invalid = true
		}
		if b.ChannelID <= 0 || len(b.GroupMap) == 0 || len(b.GroupMap) > 64 {
			s.invalid = true
		}
		for id, name := range b.GroupMap {
			n, e := strconv.Atoi(id)
			if e != nil || n <= 0 || name == "" || len(name) > 64 {
				s.invalid = true
			}
		}
	}
	if s.invalid {
		s.bindings = nil
	}
	return s
}

func (s *Service) Register(user, root *gin.RouterGroup) {
	user.GET("/account-pool", s.List)
	root.GET("/account-pool", s.AdminStatus)
}

func (s *Service) List(c *gin.Context) {
	if !s.requireEnabled(c) {
		return
	}
	user, err := model.GetUserCache(c.GetInt("id"))
	if err != nil {
		c.JSON(503, gin.H{"success": false, "message": "Account pool is unavailable"})
		return
	}
	allowed := service.GetUserUsableGroups(user.Group)
	result := s.snapshot(c.Request.Context())
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if !slices.Contains([]int{10, 20, 50}, size) {
		size = 20
	}
	q := strings.ToLower(strings.TrimSpace(c.Query("q")))
	if len(q) > 80 {
		c.JSON(400, gin.H{"success": false, "message": "Invalid account pool filter"})
		return
	}
	items := make([]Item, 0)
	summaries := map[string]*GroupSummary{}
	for _, item := range result.items {
		visible := make([]Group, 0, len(item.Groups))
		for _, g := range item.Groups {
			if _, ok := allowed[g.Name]; ok {
				g.Ratio = service.GetUserGroupRatio(user.Group, g.Name)
				visible = append(visible, g)
			}
		}
		if len(visible) == 0 {
			continue
		}
		item.Groups = visible
		if q != "" && !strings.Contains(strings.ToLower(item.AnonymousID+" "+item.Provider+" "+item.AuthType+" "+item.Plan), q) {
			continue
		}
		if v := c.Query("type"); v != "" && v != "all" && v != item.Plan {
			continue
		}
		if v := c.Query("status"); v != "" && v != "all" && v != item.Status {
			continue
		}
		if v := c.Query("group"); v != "" && v != "all" {
			found := false
			for _, g := range visible {
				if g.Name == v {
					found = true
				}
			}
			if !found {
				continue
			}
		}
		items = append(items, item)
		for _, g := range visible {
			summary := summaries[g.Name]
			if summary == nil {
				summary = &GroupSummary{Group: g, Concurrency: new(int), ConcurrencyLimit: new(int)}
				summaries[g.Name] = summary
			}
			summary.Total++
			if item.Status == "available" {
				summary.Available++
			}
			if item.Concurrency == nil {
				summary.Concurrency = nil
			} else if summary.Concurrency != nil {
				*summary.Concurrency += *item.Concurrency
			}
			if item.ConcurrencyLimit == nil {
				summary.ConcurrencyLimit = nil
			} else if summary.ConcurrencyLimit != nil {
				*summary.ConcurrencyLimit += *item.ConcurrencyLimit
			}
		}
	}
	groups := make([]GroupSummary, 0, len(summaries))
	for _, g := range summaries {
		groups = append(groups, *g)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	state := "ready"
	if !result.configured {
		state = "unconfigured"
	} else if result.fetched.IsZero() {
		state = "unavailable"
	} else if s.now().Sub(result.fetched) >= cacheTTL || !result.retryAt.IsZero() {
		state = "stale"
	}
	updated := int64(0)
	if !result.fetched.IsZero() {
		updated = result.fetched.Unix()
	}
	total := len(items)
	start := min((min(page, 100000)-1)*size, total)
	end := min(start+size, total)
	// A collection may have waited on upstream I/O while another node disabled
	// the page. Recheck the authoritative option before releasing any snapshot.
	if !s.requireEnabled(c) {
		return
	}
	common.ApiSuccess(c, Snapshot{Configured: result.configured, State: state, UpdatedAt: updated, CacheTTLSeconds: 60, StaleAfterSeconds: 300, Items: items[start:end], Total: total, Page: page, PageSize: size, Groups: groups})
}

func (s *Service) AdminStatus(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	enabled, err := s.enabled(c.Request.Context())
	if err != nil {
		s.clearCache()
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "Account pool is unavailable"})
		return
	}
	if !enabled {
		s.clearCache()
		// Root can inspect configuration without enabling the public page. This
		// path never collects upstream data or returns cached node information.
		common.ApiSuccess(c, gin.H{"enabled": false, "configured": len(s.bindings) > 0 || s.invalid, "invalid_config": s.invalid, "nodes": []NodeStatus{}, "cache_scope": "per_process", "cache_ttl_seconds": 60, "stale_after_seconds": 300, "automatic_collection": false})
		return
	}
	result := s.snapshot(c.Request.Context())
	if !s.requireEnabled(c) {
		return
	}
	common.ApiSuccess(c, gin.H{"enabled": true, "configured": result.configured, "invalid_config": s.invalid, "nodes": result.nodes, "cache_scope": "per_process", "cache_ttl_seconds": 60, "stale_after_seconds": 300, "automatic_collection": false})
}

func (s *Service) clearCache() {
	s.mu.Lock()
	s.cache = cached{}
	s.mu.Unlock()
}

func (s *Service) requireEnabled(c *gin.Context) bool {
	c.Header("Cache-Control", "no-store")
	enabled, err := s.enabled(c.Request.Context())
	if err != nil {
		s.clearCache()
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "Account pool is unavailable"})
		return false
	}
	if !enabled {
		s.clearCache()
		c.JSON(http.StatusForbidden, gin.H{"success": false, "code": "ACCOUNT_POOL_DISABLED", "message": "Account pool is disabled"})
		return false
	}
	return true
}

// Per-process singleflight and a one-minute success/failure TTL bound reads.
// N app nodes may each collect once per minute; no distributed cron is started.
func (s *Service) snapshot(ctx context.Context) cached {
	if enabled, err := s.enabled(ctx); err != nil || !enabled {
		s.clearCache()
		return cached{}
	}
	s.mu.Lock()
	previous := s.cache
	s.mu.Unlock()
	if !previous.retryAt.IsZero() && s.now().Before(previous.retryAt) {
		return s.visibleCache(previous)
	}
	if !previous.fetched.IsZero() && s.now().Sub(previous.fetched) < cacheTTL {
		return previous
	}
	ch := s.flight.DoChan("pool", func() (any, error) {
		if enabled, err := s.enabled(ctx); err != nil || !enabled {
			s.clearCache()
			return cached{}, nil
		}
		s.mu.Lock()
		current := s.cache
		s.mu.Unlock()
		if s.now().Before(current.retryAt) || (!current.fetched.IsZero() && s.now().Sub(current.fetched) < cacheTTL) {
			return current, nil
		}
		next := cached{configured: len(s.bindings) > 0 || s.invalid, items: []Item{}, nodes: []NodeStatus{}}
		if !next.configured {
			return next, nil
		}
		failed := s.invalid
		seen := map[string]bool{}
		for _, binding := range s.bindings {
			node := NodeStatus{ChannelID: binding.ChannelID, State: "unconfigured"}
			channel, err := s.channel(binding.ChannelID)
			key := s.getenv("SUB2_POOL_ADMIN_KEY_" + strconv.Itoa(binding.ChannelID))
			if err != nil || channel == nil || channel.Type != constant.ChannelTypeSub2API || channel.Status != common.ChannelStatusEnabled || channel.BaseURL == nil {
				node.ErrorCode = "channel_unavailable"
				failed = true
				next.nodes = append(next.nodes, node)
				continue
			}
			origin, err := poolOrigin(*channel.BaseURL)
			pinnedOrigin, pinErr := poolOrigin(binding.Origin)
			if err != nil || pinErr != nil || origin != pinnedOrigin {
				node.ErrorCode = "pinned_origin_mismatch"
				failed = true
				next.nodes = append(next.nodes, node)
				continue
			}
			if seen[origin] {
				node.ErrorCode = "invalid_or_duplicate_origin"
				failed = true
				next.nodes = append(next.nodes, node)
				continue
			}
			seen[origin] = true
			if key == "" {
				node.ErrorCode = "management_read_access_not_configured"
				next.configured = false
				failed = true
				next.nodes = append(next.nodes, node)
				continue
			}
			fetchCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			items, err := s.collect(fetchCtx, pinnedOrigin, key, binding)
			cancel()
			if err != nil {
				node.State, node.ErrorCode = "unavailable", "upstream_unavailable"
				failed = true
			} else {
				node.State, node.Count = "ready", len(items)
				next.items = append(next.items, items...)
			}
			next.nodes = append(next.nodes, node)
		}
		if failed {
			current.configured = next.configured
			current.nodes = next.nodes
			current.retryAt = s.now().Add(cacheTTL)
			next = current
		} else {
			next.fetched = s.now()
		}
		if enabled, err := s.enabled(ctx); err != nil || !enabled {
			s.clearCache()
			return cached{}, nil
		}
		s.mu.Lock()
		s.cache = next
		s.mu.Unlock()
		return next, nil
	})
	select {
	case <-ctx.Done():
		return s.visibleCache(previous)
	case r := <-ch:
		if r.Err != nil {
			return s.visibleCache(previous)
		}
		return s.visibleCache(r.Val.(cached))
	}
}
func (s *Service) visibleCache(value cached) cached {
	if !value.configured || (!value.fetched.IsZero() && s.now().Sub(value.fetched) > maxStale) {
		value.items = []Item{}
		value.fetched = time.Time{}
	}
	return value
}

func poolOrigin(value string) (string, error) {
	u, err := url.Parse(strings.TrimRight(value, "/"))
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Host == "" || (u.Path != "" && u.Path != "/v1") {
		return "", errors.New("invalid origin")
	}
	ip := net.ParseIP(u.Hostname())
	loopback := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return "", errors.New("HTTPS required except loopback")
	}
	u.Path = ""
	return u.String(), nil
}

// Narrow decoding discards credentials, emails, names, notes, proxy details,
// raw error messages and unmapped group metadata immediately. Never log body.
type remoteAccount struct {
	ID                 int64      `json:"id"`
	Platform           string     `json:"platform"`
	Type               string     `json:"type"`
	Status             string     `json:"status"`
	Schedulable        *bool      `json:"schedulable"`
	Concurrency        *int       `json:"concurrency"`
	CurrentConcurrency *int       `json:"current_concurrency"`
	GroupIDs           []int64    `json:"group_ids"`
	LastUsedAt         *time.Time `json:"last_used_at"`
	ExpiresAt          *int64     `json:"expires_at"`
	AutoPause          bool       `json:"auto_pause_on_expired"`
	RateLimitResetAt   *time.Time `json:"rate_limit_reset_at"`
	OverloadUntil      *time.Time `json:"overload_until"`
	TempUntil          *time.Time `json:"temp_unschedulable_until"`
	RPMPaused          bool       `json:"rpm_paused"`
	Credentials        struct {
		Plan string `json:"plan_type"`
	} `json:"credentials"`
	Extra struct {
		Usage5h *float64 `json:"codex_5h_used_percent"`
		Usage7d *float64 `json:"codex_7d_used_percent"`
	} `json:"extra"`
}

func (s *Service) collect(ctx context.Context, origin, key string, b Binding) ([]Item, error) {
	items := make([]Item, 0)
	seen := map[int64]bool{}
	total := -1
	for page := 1; page <= 50; page++ {
		// Stop pagination after an operator disables the feature. Requests already
		// sent cannot be recalled; their response is discarded by snapshot/List.
		if enabled, err := s.enabled(ctx); err != nil || !enabled {
			return nil, errors.New("account pool disabled or unavailable")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/api/v1/admin/accounts?lite=true&page=%d&page_size=100", origin, page), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("x-api-key", key)
		req.Header.Set("Accept", "application/json")
		resp, err := s.client.Do(req)
		if err != nil {
			return nil, errors.New("upstream unavailable")
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20+1))
		resp.Body.Close()
		if readErr != nil || resp.StatusCode != 200 || len(body) > 4<<20 {
			return nil, errors.New("invalid response")
		}
		var payload struct {
			Code *int `json:"code"`
			Data struct {
				Items []remoteAccount `json:"items"`
				Total int             `json:"total"`
				Page  int             `json:"page"`
			} `json:"data"`
		}
		if common.Unmarshal(body, &payload) != nil || payload.Code == nil || *payload.Code != 0 || payload.Data.Total < 0 || payload.Data.Total > maxAccounts || payload.Data.Page != page || len(payload.Data.Items) > 100 {
			return nil, errors.New("unsupported response")
		}
		if total < 0 {
			total = payload.Data.Total
		} else if total != payload.Data.Total {
			return nil, errors.New("pool changed during pagination")
		}
		for _, account := range payload.Data.Items {
			if account.ID <= 0 || seen[account.ID] {
				return nil, errors.New("invalid account identity")
			}
			seen[account.ID] = true
			groups := []Group{}
			names := map[string]bool{}
			for _, id := range account.GroupIDs {
				if name := b.GroupMap[strconv.FormatInt(id, 10)]; name != "" && !names[name] {
					groups = append(groups, Group{Name: name})
					names[name] = true
				}
			}
			if len(groups) == 0 {
				continue
			}
			sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
			mac := hmac.New(sha256.New, []byte(common.SessionSecret))
			fmt.Fprintf(mac, "sub2-pool-v1:%d:%d", b.ChannelID, account.ID)
			item := Item{AnonymousID: fmt.Sprintf("AC-%X", mac.Sum(nil)[:6]), Provider: "unknown", AuthType: "unknown", Plan: "unknown", Status: remoteStatus(account, s.now()), Groups: groups, Concurrency: validCapacity(account.CurrentConcurrency), ConcurrencyLimit: validCapacity(account.Concurrency), Usage5hPercent: validPercent(account.Extra.Usage5h), Usage7dPercent: validPercent(account.Extra.Usage7d)}
			if slices.Contains([]string{"openai", "anthropic", "gemini", "antigravity", "grok"}, account.Platform) {
				item.Provider = account.Platform
			}
			if slices.Contains([]string{"oauth", "apikey", "setup-token"}, account.Type) {
				item.AuthType = account.Type
			}
			plan := strings.ToLower(account.Credentials.Plan)
			if slices.Contains([]string{"free", "plus", "pro", "team", "enterprise", "promax"}, plan) {
				item.Plan = plan
			}
			if account.LastUsedAt != nil {
				stamp := account.LastUsedAt.Unix()
				item.LastUsedAt = &stamp
			}
			items = append(items, item)
		}
		if len(seen) == total {
			return items, nil
		}
		if len(payload.Data.Items) == 0 {
			return nil, errors.New("incomplete pagination")
		}
	}
	return nil, errors.New("pool too large")
}
func validCapacity(value *int) *int {
	if value == nil || *value < 0 || *value > 100000 {
		return nil
	}
	return value
}
func validPercent(value *float64) *float64 {
	if value == nil || *value < 0 || *value > 100 {
		return nil
	}
	return value
}
func remoteStatus(a remoteAccount, now time.Time) string {
	if a.Status == "disabled" || a.Status == "paused" {
		return "paused"
	}
	if a.Status == "error" {
		return "error"
	}
	if a.Status != "active" {
		return "unknown"
	}
	if a.RPMPaused || (a.RateLimitResetAt != nil && a.RateLimitResetAt.After(now)) {
		return "rate_limited"
	}
	if (a.TempUntil != nil && a.TempUntil.After(now)) || (a.OverloadUntil != nil && a.OverloadUntil.After(now)) || (a.AutoPause && a.ExpiresAt != nil && *a.ExpiresAt <= now.Unix()) || (a.Schedulable != nil && !*a.Schedulable) {
		return "unavailable"
	}
	if a.Schedulable == nil {
		return "unknown"
	}
	return "available"
}
