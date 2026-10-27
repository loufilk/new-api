package monitor

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

func (s *Service) Register(group *gin.RouterGroup) {
	group.GET("", s.getSnapshot)
	group.GET("/results/:id", s.getDetail)
	group.GET("/artworks/:id", s.getAsset)
	group.GET("/thumbnails/:id", s.getAsset)
}

func (s *Service) snapshot(ctx context.Context) (Snapshot, error) {
	config, err := s.config()
	if err != nil {
		return Snapshot{}, err
	}
	return s.snapshotWithConfig(ctx, config)
}

func (s *Service) snapshotWithConfig(ctx context.Context, config Config) (Snapshot, error) {
	data := Snapshot{Enabled: s.enabled && config.Enabled, Title: config.Title, Description: config.Description, Targets: []Target{}, Results: []Result{}}
	if !data.Enabled {
		return data, nil
	}
	response, stale, err := s.fetch(ctx, "", "", 30*time.Second)
	if err != nil {
		return data, err
	}
	var payload envelope[upstreamSnapshot]
	if common.Unmarshal(response.body, &payload) != nil {
		return data, errUnavailable
	}
	data.Enabled = *payload.Data.Enabled
	data.Running = payload.Data.Running
	data.ObservedAt = payload.Data.ServerTime
	data.FetchedAt = response.fetched.Unix()
	if s.now().Unix()-data.ObservedAt > int64(maxStale/time.Second) || data.ObservedAt > s.now().Unix()+300 {
		return Snapshot{}, errUnavailable
	}
	data.Stale = stale || s.now().Unix()-data.ObservedAt > 180
	if !data.Enabled {
		return data, nil
	}
	ids := make(map[string]string)
	for index, target := range payload.Data.Targets {
		// Stable opaque identities keep administrator labels on the same source
		// group when the upstream list is reordered or a new group is inserted.
		digest := sha256.Sum256([]byte(target.ID))
		id := fmt.Sprintf("route-%x", digest[:16])
		if config.HiddenGroups[id] {
			continue
		}
		ids[target.ID] = id
		data.Targets = append(data.Targets, Target{ID: id, Number: index + 1, Model: publicText(target.Model), Name: config.Groups[id]})
	}
	for _, result := range payload.Data.Results {
		id, ok := ids[result.TargetID]
		if !ok || !resultIDPattern.MatchString(result.ID) || (result.Benchmark != "pelican" && result.Benchmark != "candy") {
			continue
		}
		result.TargetID = id
		switch result.Status {
		case "ready", "passed", "incorrect", "running", "queued", "failed", "interrupted", "review":
		default:
			result.Status = "unknown"
		}
		switch result.QualityStatus {
		case "passed", "failed", "suspicious", "review", "running":
		default:
			result.QualityStatus = ""
		}
		result.QualityReason = publicText(result.QualityReason)
		data.Results = append(data.Results, result)
	}
	return data, nil
}

func (s *Service) getSnapshot(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	data, err := s.snapshot(c.Request.Context())
	if err != nil {
		unavailable(c)
		return
	}
	c.JSON(http.StatusOK, envelope[Snapshot]{Success: true, Data: data})
}

// Only IDs in the current public snapshot can reach upstream. This also keeps
// arbitrary cache keys and retired/private result enumeration out of the proxy.
func (s *Service) selectedResult(c *gin.Context) (Result, bool) {
	if !s.enabled {
		c.Status(http.StatusNotFound)
		return Result{}, false
	}
	id := c.Param("id")
	if !resultIDPattern.MatchString(id) {
		c.Status(http.StatusNotFound)
		return Result{}, false
	}
	snapshot, err := s.snapshot(c.Request.Context())
	if err != nil {
		unavailable(c)
		return Result{}, false
	}
	for _, result := range snapshot.Results {
		if result.ID == id {
			return result, true
		}
	}
	c.Status(http.StatusNotFound)
	return Result{}, false
}

func (s *Service) getDetail(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	result, ok := s.selectedResult(c)
	if !ok {
		return
	}
	version := fmt.Sprintf("%d:%s:%s:%d", result.Revision, result.Status, result.QualityStatus, result.CompletedAt)
	response, stale, err := s.fetch(c.Request.Context(), "/results/"+result.ID, version, time.Minute)
	if err != nil {
		unavailable(c)
		return
	}
	var payload envelope[Detail]
	if common.Unmarshal(response.body, &payload) != nil {
		unavailable(c)
		return
	}
	payload.Data.Prompt = publicText(payload.Data.Prompt)
	payload.Data.Answer = publicText(payload.Data.Answer)
	if result.Benchmark == "pelican" {
		payload.Data.Answer = ""
	}
	payload.Data.ReferenceReason = publicText(payload.Data.ReferenceReason)
	c.JSON(http.StatusOK, gin.H{"success": true, "data": payload.Data, "stale": stale, "fetched_at": response.fetched.Unix()})
}

func (s *Service) getAsset(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	result, ok := s.selectedResult(c)
	if !ok {
		return
	}
	if result.Benchmark != "pelican" || result.Status != "ready" {
		c.Status(http.StatusNotFound)
		return
	}
	kind, contentType := "/thumbnails/", "image/png"
	if c.FullPath() == "/api/model-monitor/artworks/:id" {
		kind, contentType = "/artworks/", "image/svg+xml; charset=utf-8"
	}
	version := strconv.Itoa(result.Revision) + ":" + strconv.FormatInt(result.CompletedAt, 10)
	response, _, err := s.fetch(c.Request.Context(), kind+result.ID, version, 5*time.Minute)
	if err != nil {
		unavailable(c)
		return
	}
	// The image element has no script privileges. These headers additionally
	// constrain direct navigation to an SVG, including CSS/external resources.
	c.Header("Content-Security-Policy", "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("Cross-Origin-Resource-Policy", "same-origin")
	c.Data(http.StatusOK, contentType, response.body)
}

func unavailable(c *gin.Context) {
	c.Header("Retry-After", "15")
	c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": errUnavailable.Error()})
}
