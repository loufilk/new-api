package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminUpdateSubscriptionPlanGroupFields(t *testing.T) {
	for _, tc := range []struct {
		name string
		fields string
		wantSuccess bool
		wantUpgrade string
		wantDowngrade string
	}{
		{"omitted", "", true, "new", "new"},
		{"null preserves", `,"upgrade_group":null,"downgrade_group":null`, true, "new", "new"},
		{"explicit clear upgrade", `,"upgrade_group":""`, true, "", "new"},
			{"explicit clear downgrade", `,"downgrade_group":""`, true, "new", ""},
		{"database group despite stale runtime", `,"upgrade_group":" new ","downgrade_group":"new"`, true, "new", "new"},
		{"retired upgrade", `,"upgrade_group":"old"`, false, "new", "new"},
		{"retired downgrade", `,"downgrade_group":"old"`, false, "new", "new"},
		{"unknown group", `,"upgrade_group":"unknown"`, false, "new", "new"},
		{"non string", `,"upgrade_group":12`, false, "new", "new"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previousDB, previousLog := model.DB, model.LOG_DB
			previousMain, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
			previousRedis := common.RedisEnabled
			t.Cleanup(func() {
				model.DB, model.LOG_DB = previousDB, previousLog
				common.SetDatabaseTypes(previousMain, previousLogType)
				common.RedisEnabled = previousRedis
			})
			db := setupGroupRenameControllerTest(t)
			payment := operation_setting.GetPaymentSetting()
			previousPayment := *payment
			t.Cleanup(func() { *payment = previousPayment })
			payment.ComplianceConfirmed = true
			payment.ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
			require.NoError(t, db.Create(&model.Option{Key: "GroupRatio", Value: `{"new":1}`}).Error)
			require.NoError(t, db.Create(&model.Option{Key: "GroupRenameAliases", Value: `{"old":"new"}`}).Error)
			plan := model.SubscriptionPlan{Title: "original", UpgradeGroup: "new", DowngradeGroup: "new", Enabled: true, PriceAmount: 9}
			require.NoError(t, db.Create(&plan).Error)
			// The retired key still appears valid to this process. The database
			// namespace must reject it and accept new even with this stale cache.
			require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"new":1,"old":1,"unknown":1}`))

			router := gin.New()
			router.PUT("/plans/:id", AdminUpdateSubscriptionPlan)
			body := `{"plan":{"title":"edited","price_amount":0,"enabled":false` + tc.fields + `}}`
			request := httptest.NewRequest(http.MethodPut, "/plans/1", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			var response struct { Success bool `json:"success"`; Message string `json:"message"` }
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			require.Equal(t, tc.wantSuccess, response.Success, response.Message)
			var stored model.SubscriptionPlan
			require.NoError(t, db.First(&stored, plan.Id).Error)
			assert.Equal(t, tc.wantUpgrade, stored.UpgradeGroup)
			assert.Equal(t, tc.wantDowngrade, stored.DowngradeGroup)
			if tc.wantSuccess {
				assert.Equal(t, "edited", stored.Title)
				assert.Zero(t, stored.PriceAmount)
				assert.False(t, stored.Enabled)
			} else {
				assert.Equal(t, "original", stored.Title)
				assert.Equal(t, float64(9), stored.PriceAmount)
				assert.True(t, stored.Enabled)
			}
		})
	}
}

func TestAdminUpdateSubscriptionPlanOmittedGroupsPreservePersistedLegacyValues(t *testing.T) {
	previousDB, previousLog := model.DB, model.LOG_DB
	previousMain, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousRedis := common.RedisEnabled
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLog
		common.SetDatabaseTypes(previousMain, previousLogType)
		common.RedisEnabled = previousRedis
	})

	db := setupGroupRenameControllerTest(t)
	payment := operation_setting.GetPaymentSetting()
	previousPayment := *payment
	t.Cleanup(func() { *payment = previousPayment })
	payment.ComplianceConfirmed = true
	payment.ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion

	// Seed a plan while "old" is still configured, then simulate persisted
	// legacy state after that group has been retired. A non-group edit must not
	// revalidate or rewrite the omitted group fields.
	require.NoError(t, db.Create(&model.Option{Key: "GroupRatio", Value: `{"old":1}`}).Error)
	plan := model.SubscriptionPlan{
		Title:          "original",
		UpgradeGroup:   "old",
		DowngradeGroup: "old",
		Enabled:        true,
		PriceAmount:    9,
	}
	require.NoError(t, db.Create(&plan).Error)
	require.NoError(t, db.Model(&model.Option{}).
		Where("key = ?", "GroupRatio").
		Update("value", `{"new":1}`).Error)
	require.NoError(t, db.Create(&model.Option{Key: "GroupRenameAliases", Value: `{"old":"new"}`}).Error)
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"new":1}`))

	router := gin.New()
	router.PUT("/plans/:id", AdminUpdateSubscriptionPlan)
	request := httptest.NewRequest(
		http.MethodPut,
		"/plans/1",
		strings.NewReader(`{"plan":{"title":"edited","price_amount":0,"enabled":false}}`),
	)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success, response.Message)

	var stored model.SubscriptionPlan
	require.NoError(t, db.First(&stored, plan.Id).Error)
	assert.Equal(t, "edited", stored.Title)
	assert.Zero(t, stored.PriceAmount)
	assert.False(t, stored.Enabled)
	assert.Equal(t, "old", stored.UpgradeGroup)
	assert.Equal(t, "old", stored.DowngradeGroup)
}
