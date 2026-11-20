package controller

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/console_setting"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
)

const (
	requestTimeout      = 30 * time.Second
	httpTimeout         = 10 * time.Second
	uptimeKeySuffix     = "_24"
	apiStatusPath       = "/api/status-page/"
	apiHeartbeatPath    = "/api/status-page/heartbeat/"
	uptimeResponseLimit = 2 << 20
	uptimeFreshFor      = 20 * time.Second
	uptimeStaleFor      = 5 * time.Minute
)

type Monitor struct {
	Name   string  `json:"name"`
	Uptime float64 `json:"uptime"`
	Status int     `json:"status"`
	Group  string  `json:"group,omitempty"`
}

type UptimeGroupResult struct {
	CategoryName string    `json:"categoryName"`
	Monitors     []Monitor `json:"monitors"`
	State        string    `json:"state"`
	UpdatedAt    int64     `json:"updated_at,omitempty"`
}

func getAndDecode(ctx context.Context, client *http.Client, url string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return errors.New("non-200 status")
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, uptimeResponseLimit+1))
	if err != nil {
		return err
	}
	if len(body) > uptimeResponseLimit {
		return errors.New("upstream status response exceeds limit")
	}
	return json.Unmarshal(body, dest)
}

func fetchGroupData(ctx context.Context, client *http.Client, groupConfig map[string]any) UptimeGroupResult {
	rawURL, _ := groupConfig["url"].(string)
	slug, _ := groupConfig["slug"].(string)
	categoryName, _ := groupConfig["categoryName"].(string)

	result := UptimeGroupResult{
		CategoryName: categoryName,
		Monitors:     []Monitor{},
		State:        "unavailable",
	}

	base, err := url.Parse(rawURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" ||
		base.User != nil || base.RawQuery != "" || base.Fragment != "" || slug == "" {
		return result
	}
	buildURL := func(part string) string {
		endpoint := *base
		endpoint.Path = strings.TrimRight(endpoint.Path, "/") + part + slug
		endpoint.RawPath = ""
		return endpoint.String()
	}

	var statusData struct {
		PublicGroupList []struct {
			ID          int    `json:"id"`
			Name        string `json:"name"`
			MonitorList []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"monitorList"`
		} `json:"publicGroupList"`
	}

	var heartbeatData struct {
		HeartbeatList map[string][]struct {
			Status int `json:"status"`
		} `json:"heartbeatList"`
		UptimeList map[string]float64 `json:"uptimeList"`
	}

	g, gCtx := errgroup.WithContext(ctx)
	g.Go(func() error {
		return getAndDecode(gCtx, client, buildURL(apiStatusPath), &statusData)
	})
	g.Go(func() error {
		return getAndDecode(gCtx, client, buildURL(apiHeartbeatPath), &heartbeatData)
	})

	if g.Wait() != nil || statusData.PublicGroupList == nil || heartbeatData.HeartbeatList == nil {
		return result
	}

	result.State = "ready"
	for _, pg := range statusData.PublicGroupList {
		if len(pg.MonitorList) == 0 {
			continue
		}

		for _, m := range pg.MonitorList {
			monitor := Monitor{
				Name:   m.Name,
				Group:  pg.Name,
				Status: -1, // no heartbeat means unknown, never infer down
			}

			monitorID := strconv.Itoa(m.ID)

			if uptime, exists := heartbeatData.UptimeList[monitorID+uptimeKeySuffix]; exists {
				monitor.Uptime = uptime
			}

			if heartbeats, exists := heartbeatData.HeartbeatList[monitorID]; exists && len(heartbeats) > 0 {
				monitor.Status = heartbeats[0].Status
			}

			result.Monitors = append(result.Monitors, monitor)
		}
	}

	return result
}

type uptimeStatusSnapshot struct {
	key       string
	expiresAt time.Time
	results   []UptimeGroupResult
}

type uptimeStatusCache struct {
	mu     sync.Mutex
	latest uptimeStatusSnapshot
	flight singleflight.Group
}

var uptimeCache uptimeStatusCache

func (cache *uptimeStatusCache) load(ctx context.Context, key string, groups []map[string]any, client *http.Client) ([]UptimeGroupResult, error) {
	cache.mu.Lock()
	previous := cache.latest
	cache.mu.Unlock()
	if previous.key == key && time.Now().Before(previous.expiresAt) {
		return previous.results, nil
	}

	ch := cache.flight.DoChan(key, func() (any, error) {
		cache.mu.Lock()
		previous := cache.latest
		cache.mu.Unlock()
		if previous.key == key && time.Now().Before(previous.expiresAt) {
			return previous.results, nil
		}

		// A refresh is shared by viewers. Cancellation of any one browser
		// request must not cancel the refresh for everyone else.
		work, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		results := make([]UptimeGroupResult, len(groups))
		group, groupCtx := errgroup.WithContext(work)
		group.SetLimit(4)
		for i, spec := range groups {
			group.Go(func() error {
				results[i] = fetchGroupData(groupCtx, client, spec)
				return nil
			})
		}
		_ = group.Wait()
		now := time.Now()
		for i := range results {
			if results[i].State == "ready" {
				results[i].UpdatedAt = now.Unix()
				continue
			}
			if previous.key == key && i < len(previous.results) {
				last := previous.results[i]
				if last.UpdatedAt > 0 && now.Sub(time.Unix(last.UpdatedAt, 0)) < uptimeStaleFor {
					last.State = "stale"
					results[i] = last
				}
			}
		}
		cache.mu.Lock()
		cache.latest = uptimeStatusSnapshot{key: key, expiresAt: now.Add(uptimeFreshFor), results: results}
		cache.mu.Unlock()
		return results, nil
	})

	select {
	case result := <-ch:
		if result.Err != nil {
			return nil, result.Err
		}
		return result.Val.([]UptimeGroupResult), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

var uptimeClient = sync.OnceValue(func() *http.Client {
	client, _ := service.NewSSRFProtectedHTTPClientWithProxy("")
	client.Timeout = httpTimeout
	return client
})

func GetUptimeKumaStatus(c *gin.Context) {
	common.OptionMapRWMutex.RLock()
	enabled := console_setting.GetConsoleSetting().UptimeKumaEnabled
	groups := console_setting.GetUptimeKumaGroups()
	common.OptionMapRWMutex.RUnlock()
	if !enabled || len(groups) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": []UptimeGroupResult{}})
		return
	}

	// Bound legacy configurations as well as settings going through validation.
	groups = groups[:min(len(groups), 20)]
	key, err := common.Marshal(groups)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "data": []UptimeGroupResult{}})
		return
	}
	results, err := uptimeCache.load(c.Request.Context(), string(key), groups, uptimeClient())
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "data": []UptimeGroupResult{}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": results})
}
