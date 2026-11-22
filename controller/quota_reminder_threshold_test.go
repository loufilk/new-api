package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuotaReminderProfileThresholdValidationPreservesLegacyFractions(t *testing.T) {
	for _, tc := range []struct {
		name, field string
		success     bool
		want        float64
	}{
		{"omitted_preserves_fraction", "", true, 1234.5},
		{"unchanged_legacy_fraction", `,"quota_warning_threshold":1234.5`, true, 1234.5},
		{"new_finite_fraction_remains_compatible", `,"quota_warning_threshold":12.75`, true, 12.75},
		{"explicit_zero_uses_site_default", `,"quota_warning_threshold":0`, true, 0},
		{"finite_safe_upper_bound", `,"quota_warning_threshold":9007199254740991`, true, 9007199254740991},
		{"null_not_a_zero_sentinel", `,"quota_warning_threshold":null`, false, 1234.5},
		{"negative", `,"quota_warning_threshold":-1`, false, 1234.5},
		{"unsafe_magnitude", `,"quota_warning_threshold":9007199254740992`, false, 1234.5},
		{"huge_finite", `,"quota_warning_threshold":1e308`, false, 1234.5},
		{"numeric_overflow", `,"quota_warning_threshold":1e309`, false, 1234.5},
		{"string_infinity", `,"quota_warning_threshold":"Infinity"`, false, 1234.5},
		{"string_nan", `,"quota_warning_threshold":"NaN"`, false, 1234.5},
		{"wrong_boolean", `,"quota_warning_threshold":false`, false, 1234.5},
		{"wrong_object", `,"quota_warning_threshold":{}`, false, 1234.5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupManageUserTestDB(t)
			original := dto.UserSetting{NotifyType: dto.NotifyTypeEmail, QuotaWarningThreshold: 1234.5, Language: "zhCN", BillingPreference: "wallet_only"}
			encoded, err := common.Marshal(original)
			require.NoError(t, err)
			user := model.User{Username: "threshold-owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AffCode: "threshold-owner", Setting: string(encoded)}
			require.NoError(t, db.Create(&user).Error)
			body := `{"notify_type":"email"` + tc.field + `}`
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPut, "/api/user/setting", strings.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set("id", user.Id)
			UpdateUserSetting(c)
			assert.Contains(t, recorder.Body.String(), fmt.Sprintf(`"success":%t`, tc.success))
			require.NoError(t, db.First(&user, user.Id).Error)
			if !tc.success {
				assert.Equal(t, string(encoded), user.Setting, "rejection must not write any saved setting")
			}
			want := original
			want.QuotaWarningThreshold = tc.want
			assert.Equal(t, want, user.GetSetting())
		})
	}
}

func TestQuotaReminderSiteOptionRejectsMalformedWireWithoutWrites(t *testing.T) {
	for _, value := range []string{`"Infinity"`, `"garbage"`, `null`, `false`, `{}`, `-1`, `1.5`, `1e308`, `"9007199254740992"`, `"9223372036854775808"`} {
		t.Run(value, func(t *testing.T) {
			db := setupManageUserTestDB(t)
			require.NoError(t, db.AutoMigrate(&model.Option{}))
			previousMap, previousThreshold := common.OptionMap, common.QuotaRemindThreshold
			common.OptionMap, common.QuotaRemindThreshold = map[string]string{}, 1234
			t.Cleanup(func() { common.OptionMap, common.QuotaRemindThreshold = previousMap, previousThreshold })
			require.NoError(t, model.UpdateOption("QuotaRemindThreshold", "1234"))
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPut, "/api/option", strings.NewReader(`{"key":"QuotaRemindThreshold","value":`+value+`}`))
			c.Request.Header.Set("Content-Type", "application/json")
			UpdateOption(c)
			assert.Contains(t, recorder.Body.String(), `"success":false`)
			var saved model.Option
			require.NoError(t, db.Where("key = ?", "QuotaRemindThreshold").First(&saved).Error)
			assert.Equal(t, "1234", saved.Value)
			assert.Equal(t, "1234", common.OptionMap["QuotaRemindThreshold"])
			assert.Equal(t, 1234, common.QuotaRemindThreshold)
		})
	}
}
