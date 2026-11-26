package console

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// PublishedLine is the common approved projection for models and health.
// Group and sampling metadata are never serializable public fields.
type PublishedLine struct {
	ID                  string   `json:"id"`
	Label               string   `json:"label"`
	Ratio               float64  `json:"ratio"`
	SortOrder           int      `json:"sort_order,omitempty"`
	Badges              []string `json:"badges,omitempty"`
	RepresentativeModel string   `json:"representative_model,omitempty"`
	IsNew               bool     `json:"is_new,omitempty"`
	PreviousRatio       *float64 `json:"previous_ratio,omitempty"`
	Group               string   `json:"-"`
	PublishHealth       bool     `json:"-"`
	ObservedSince       int64    `json:"-"`
	Vendor              string   `json:"-"`
}

type modelLineSupport struct {
	ModelName    string                             `json:"model_name"`
	LineIDs      []string                           `json:"public_line_ids"`
	EndpointType map[string][]constant.EndpointType `json:"supported_endpoint_types_by_public_line"`
}

type publishedModelCatalog struct {
	Lines  []PublishedLine    `json:"public_lines"`
	Models []modelLineSupport `json:"models"`
}

func buildPublishedModelCatalog(ctx context.Context, revision int64, configured map[string]model.PublicModelLineRecord) (publishedModelCatalog, error) {
	result := publishedModelCatalog{Lines: []PublishedLine{}, Models: []modelLineSupport{}}
	ratios := model.LivePublicModelLineGroups(configured)
	if len(ratios) == 0 {
		return result, nil
	}
	aliases := make(map[string]string, len(ratios))
	byID := make(map[string]string, len(ratios))
	rateScopes := make(map[string]bool, len(ratios))
	groups := make([]string, 0, len(ratios))
	for group := range ratios {
		id := configured[group].ID
		aliases[group], byID[id], rateScopes[group] = id, group, true
		groups = append(groups, group)
	}
	liveModels, err := model.LivePublicLineModelMembership(ctx, groups)
	if err != nil || len(liveModels) == 0 {
		return result, err
	}
	snapshot := model.GetPricingCatalogSnapshot()
	vendorLabels := make(map[int]string, len(snapshot.Vendors))
	for _, vendor := range snapshot.Vendors {
		vendorLabels[vendor.ID] = vendor.Name
	}
	active := make(map[string]bool, len(ratios))
	representatives := make(map[string]string, len(ratios))
	for _, row := range snapshot.Rows {
		if row.ModelName == "stealth/pixel-canary" {
			continue
		}
		_, protocols := row.ProjectEndpointGroups(aliases)
		membership := make([]string, 0, len(protocols))
		for id, kinds := range protocols {
			group := byID[id]
			if len(kinds) == 0 || !slices.Contains(row.EnableGroup, group) || !liveModels[group][row.ModelName] {
				delete(protocols, id)
				continue
			}
			membership = append(membership, id)
			active[group] = true
			if configured[group].RepresentativeModel == row.ModelName {
				representatives[group] = vendorLabels[row.VendorID]
			}
		}
		if len(membership) > 0 {
			slices.Sort(membership)
			result.Models = append(result.Models, modelLineSupport{row.ModelName, membership, protocols})
		}
	}
	activity, _ := model.ProjectGroupMarketActivity(revision, rateScopes)
	now := time.Now().Unix()
	for group, ratio := range ratios {
		if !active[group] {
			continue
		}
		entry := configured[group]
		line := PublishedLine{
			ID: entry.ID, Label: entry.Label, Ratio: ratio, SortOrder: entry.SortOrder,
			Badges: slices.Clone(entry.Badges), Group: group,
			IsNew: entry.PublishedAt <= now && now-entry.PublishedAt < 7*24*60*60,
		}
		if vendor, ok := representatives[group]; ok {
			line.RepresentativeModel = entry.RepresentativeModel
			line.PublishHealth, line.ObservedSince, line.Vendor = entry.PublishHealth, entry.ObservedSince, vendor
		}
		market := activity[group]
		if market.RecentRateChange && market.RateChanges[0].EffectiveAt > entry.PublishedAt {
			line.PreviousRatio = market.RateChanges[0].OldRatio
		}
		result.Lines = append(result.Lines, line)
	}
	slices.SortFunc(result.Lines, func(a, b PublishedLine) int {
		if order := cmp.Compare(a.SortOrder, b.SortOrder); order != 0 {
			return order
		}
		return cmp.Compare(a.Label, b.Label)
	})
	return result, ctx.Err()
}

func readPublishedModelCatalog(ctx context.Context) (publishedModelCatalog, error) {
	guard, err := model.BeginGroupConfigRead(ctx)
	if err != nil {
		return publishedModelCatalog{}, err
	}
	defer guard.Close()
	configured, err := model.ReadPublicModelLineRecords(ctx)
	if err != nil {
		return publishedModelCatalog{}, err
	}
	result, err := buildPublishedModelCatalog(ctx, guard.Version, configured)
	if err != nil {
		return publishedModelCatalog{}, err
	}
	err = guard.Finish()
	return result, err
}

func PublishedLines(ctx context.Context) ([]PublishedLine, error) {
	result, err := readPublishedModelCatalog(ctx)
	return result.Lines, err
}

func GetPublicModelLines(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	catalog, err := readPublishedModelCatalog(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "Public model lines are temporarily unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "public_lines": catalog.Lines, "models": catalog.Models})
}

// resolvePublicModelLine is only mounted under the authenticated self scope.
// Its output can prefill, but cannot authorize or create a token.
func resolvePublicModelLine(c *gin.Context, userGroups func(string) map[string]string) {
	c.Header("Cache-Control", "private, no-store")
	ctx := c.Request.Context()
	id, modelName := c.Query("id"), c.Query("model")
	lineNotFound := func() {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Public model line not available"})
	}
	if !strings.HasPrefix(id, "line-") || len(id) != 37 || modelName == "" || len(modelName) > 255 {
		lineNotFound()
		return
	}
	guard, err := model.BeginGroupConfigRead(ctx)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "Public model lines are temporarily unavailable"})
		return
	}
	defer guard.Close()
	configured, err := model.ReadPublicModelLineRecords(ctx)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "Public model lines are temporarily unavailable"})
		return
	}
	catalog, err := buildPublishedModelCatalog(ctx, guard.Version, configured)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "Public model lines are temporarily unavailable"})
		return
	}
	user, err := model.GetUserCache(c.GetInt("id"))
	if err != nil {
		lineNotFound()
		return
	}
	index := slices.IndexFunc(catalog.Lines, func(line PublishedLine) bool { return line.ID == id })
	if index < 0 {
		lineNotFound()
		return
	}
	group := catalog.Lines[index].Group
	_, permitted := userGroups(user.Group)[group]
	allowed := permitted && slices.ContainsFunc(catalog.Models, func(m modelLineSupport) bool {
		return m.ModelName == modelName && slices.Contains(m.LineIDs, id)
	})
	if !allowed || guard.Finish() != nil {
		lineNotFound()
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"group": group, "model": modelName}})
}
