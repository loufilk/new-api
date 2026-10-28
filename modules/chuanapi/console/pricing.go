package console

import (
	"math"
	"net/http"
	"slices"
	"strconv"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// pricingVersion identifies the anonymous catalogue projection. Published
// marketing snapshots compare it, so keep it stable across handler moves.
const pricingVersion = "public-catalog-v2"

// publicCatalogModel is the shared allowlist for every anonymous model catalog.
// Operator notes, routing identities, plugin identities and future host fields
// must be explicitly reviewed before they can become public.
type publicCatalogModel struct {
	ModelName              string                       `json:"model_name"`
	VendorID               int                          `json:"vendor_id"`
	QuotaType              int                          `json:"quota_type"`
	ModelRatio             float64                      `json:"model_ratio"`
	ModelPrice             float64                      `json:"model_price"`
	CompletionRatio        float64                      `json:"completion_ratio"`
	CacheRatio             *float64                     `json:"cache_ratio"`
	CreateCacheRatio       *float64                     `json:"create_cache_ratio"`
	ImageRatio             *float64                     `json:"image_ratio"`
	AudioRatio             *float64                     `json:"audio_ratio"`
	AudioCompletionRatio   *float64                     `json:"audio_completion_ratio"`
	EnableGroup            []string                     `json:"enable_groups"`
	SupportedEndpointTypes []constant.EndpointType      `json:"supported_endpoint_types"`
	BillingMode            string                       `json:"billing_mode"`
	BillingExpr            string                       `json:"billing_expr"`
	BillingVariantsPresent bool                         `json:"billing_variants_present"`
	PricingVersion         string                       `json:"pricing_version"`
	ContextLength          *int                         `json:"context_length"`
	MaxOutputTokens        *int                         `json:"max_output_tokens"`
	KnowledgeCutoff        string                       `json:"knowledge_cutoff"`
	ReleaseDate            string                       `json:"release_date"`
	ParameterCount         string                       `json:"parameter_count"`
	InputModalities        []string                     `json:"input_modalities"`
	OutputModalities       []string                     `json:"output_modalities"`
	Capabilities           []string                     `json:"capabilities"`
	DisplayCategories      model.ModelDisplayCategories `json:"display_categories,omitempty"`
	BonusEligible          *bool                        `json:"bonus_eligible"`
}

type publicCatalog struct {
	Models      []publicCatalogModel
	GroupRatio  map[string]float64
	UsableGroup map[string]string
	AutoGroups  []string
}

// buildPublicCatalog never consults caller identity. Both JSON pricing and AI
// documentation use the same public groups, display-only tiers and model fields.
func buildPublicCatalog() publicCatalog {
	usableGroup := setting.GetUserUsableGroupsCopy()
	ratios := ratio_setting.GetGroupRatioCopy()
	aliases := make(map[string]string)
	groupRatio := make(map[string]float64)
	publicGroups := make(map[string]string)
	for group := range usableGroup {
		ratio, ok := ratios[group]
		if !ok || math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0 {
			continue
		}
		key := "rate-" + strconv.FormatFloat(ratio, 'f', -1, 64)
		aliases[group] = key
		groupRatio[key] = ratio
		publicGroups[key] = "价格档位 ×" + strconv.FormatFloat(ratio, 'f', -1, 64)
	}
	autoGroups := make([]string, 0)
	for _, group := range setting.GetAutoGroups() {
		if group == "" || group == "auto" {
			continue
		}
		if alias, ok := aliases[group]; ok && !slices.Contains(autoGroups, alias) {
			autoGroups = append(autoGroups, alias)
		}
	}
	rows := publicPricingRows(model.FilterPricingByUsableGroups(model.GetPricing(), usableGroup), aliases)
	return publicCatalog{Models: rows, GroupRatio: groupRatio, UsableGroup: publicGroups, AutoGroups: autoGroups}
}

// GetPublicPricing serves the anonymous marketing catalogue. It always projects
// the public usable-group set and never consults a caller identity, so a stray
// cookie, bearer token, or forged context value cannot reveal private groups or
// personalized discounts.
func GetPublicPricing(c *gin.Context) {
	catalog := buildPublicCatalog()
	publicVendorIDs := make(map[int]bool)
	for _, row := range catalog.Models {
		publicVendorIDs[row.VendorID] = true
	}
	vendors := make([]gin.H, 0)
	for _, vendor := range model.GetVendors() {
		if !publicVendorIDs[vendor.ID] {
			continue
		}
		vendors = append(vendors, gin.H{"id": vendor.ID, "name": vendor.Name, "icon": vendor.Icon})
	}

	c.JSON(http.StatusOK, gin.H{
		"success":                     true,
		"data":                        catalog.Models,
		"vendors":                     vendors,
		"group_ratio":                 catalog.GroupRatio,
		"usable_group":                catalog.UsableGroup,
		"supported_endpoint":          model.GetSupportedEndpointMap(),
		"auto_groups":                 catalog.AutoGroups,
		"pricing_version":             pricingVersion,
		"group_keys_are_display_only": true,
	})
}

// Anonymous catalog fields are an allowlist. Routing identities, operator notes,
// plugin identities and future host fields must not automatically become public.
func publicPricingRows(rows []model.Pricing, aliases map[string]string) []publicCatalogModel {
	result := make([]publicCatalogModel, 0, len(rows))
	for _, row := range rows {
		if row.ModelName == "stealth/pixel-canary" {
			continue
		}
		groups := make([]string, 0)
		for _, group := range row.EnableGroup {
			if alias, ok := aliases[group]; ok && !slices.Contains(groups, alias) {
				groups = append(groups, alias)
			}
		}
		if len(groups) == 0 {
			continue
		}
		result = append(result, publicCatalogModel{
			ModelName: row.ModelName, VendorID: row.VendorID,
			QuotaType: row.QuotaType, ModelRatio: row.ModelRatio,
			ModelPrice: row.ModelPrice, CompletionRatio: row.CompletionRatio,
			CacheRatio: row.CacheRatio, CreateCacheRatio: row.CreateCacheRatio,
			ImageRatio: row.ImageRatio, AudioRatio: row.AudioRatio,
			AudioCompletionRatio: row.AudioCompletionRatio,
			EnableGroup:          groups, SupportedEndpointTypes: row.SupportedEndpointTypes,
			BillingMode: row.BillingMode, BillingExpr: row.BillingExpr,
			BillingVariantsPresent: len(row.BillingPluginVariants) > 0,
			PricingVersion:         row.PricingVersion,
			ContextLength:          row.ContextLength, MaxOutputTokens: row.MaxOutputTokens,
			KnowledgeCutoff: row.KnowledgeCutoff, ReleaseDate: row.ReleaseDate,
			ParameterCount: row.ParameterCount, InputModalities: row.InputModalities,
			OutputModalities: row.OutputModalities, Capabilities: row.Capabilities,
			BonusEligible:     row.BonusEligible,
			DisplayCategories: row.DisplayCategories,
		})
	}
	return result
}
