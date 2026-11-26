package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestIM21LockedAdminWritesAndSupplierFormatReturnConflict(t *testing.T) {
	t.Setenv(common.IM21RolloutModeEnv, "locked")
	for _, tc := range []struct {
		path    string
		method  string
		body    any
		handler gin.HandlerFunc
	}{
		{"/group-catalog", http.MethodPatch, gin.H{"revision": 0, "groups": gin.H{"vip": gin.H{"exclusive": true}}}, PatchGroupCatalog},
		{"/group-entitlements/7", http.MethodPut, gin.H{"revision": 0, "groups": []string{"vip"}}, PutUserGroupEntitlements},
		{"/user-group-pricing", http.MethodPut, gin.H{"user_id": 7, "group": "vip", "ratio": "0.25", "expected_revision": 0}, UpdateUserGroupPricing},
		{"/group-rename", http.MethodPost, gin.H{"renames": []model.GroupRenameMapping{{From: "before", To: "after"}}}, RenameGroups},
		{"/partner-applications", http.MethodPost, gin.H{"site_name": "Fixture", "contact_type": "telegram", "contact": "fixture", "quotes": []gin.H{{"platform": "openai", "group": "standard", "rate": "1"}}}, CreatePartnerApplication},
	} {
		t.Run(tc.path, func(t *testing.T) {
			ctx, rec := newGroupRenameContext(t, tc.method, tc.path, tc.body, 1)
			ctx.Request.Header.Set("Content-Type", "application/json")
			ctx.Request.Header.Set("Idempotency-Key", "rollout-test")
			ctx.Params = gin.Params{{Key: "user_id", Value: "7"}}
			tc.handler(ctx)
			require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
			var result struct {
				Success bool   `json:"success"`
				Code    string `json:"code"`
			}
			require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &result))
			require.False(t, result.Success)
			require.Equal(t, "IM21_ROLLOUT_LOCKED", result.Code)
		})
	}
}
