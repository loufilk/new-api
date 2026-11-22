package controller

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exercise the real JSON handler and SQL wallet, not a decoder-only mock.
// Missing/null override values are the regression targets; all other leaves
// preserve existing explicit-zero, non-quota and invalid-number semantics.
func TestManageUserQuotaInputPresenceAndType(t *testing.T) {
	for _, tc := range []struct {
		name, valueField string
		nonQuota, allow  bool
	}{
		{name: "missing_override"},
		{name: "null_override", valueField: `,"value":null`},
		{name: "explicit_zero_override", valueField: `,"value":0`, allow: true},
		{name: "fractional_override", valueField: `,"value":0.5`},
		{name: "nonfinite_exponent_override", valueField: `,"value":1e309`},
		{name: "unsafe_integer_override", valueField: `,"value":9007199254740992`},
		{name: "numeric_string_override", valueField: `,"value":"0"`},
		{name: "missing_nonquota", nonQuota: true, allow: true},
		{name: "null_nonquota", valueField: `,"value":null`, nonQuota: true, allow: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupManageUserTestDB(t)
			user := model.User{
				Username: "quota-input-owner", AffCode: "quota-input-owner-aff",
				Role: common.RoleCommonUser, Status: common.UserStatusDisabled,
				Group: "default", AuthVersion: 1,
				Quota: 1500, QuotaPaid: 1000, QuotaBonus: 300, QuotaReward: 200,
			}
			require.NoError(t, db.Create(&user).Error)
			requestID := "quota-input-" + tc.name
			action, mode := "add_quota", `,"mode":"override"`
			var identity service.AuthIdentity
			var proof string
			if tc.nonQuota {
				action, mode = "enable", ""
				identity, proof = manageUserProof(t, db, service.VerificationOperation{
					Scope:   service.VerificationScopeAdminUserManage,
					Context: []byte(fmt.Sprintf(`{"user_id":%d,"action":"enable"}`, user.Id)),
				})
			} else {
				createQuotaTestOperator(t, db, common.RoleRootUser)
				identity = service.AuthIdentity{UserID: 9999}
			}
			body := fmt.Sprintf(`{"id":%d,"action":%q,"request_id":%q%s%s}`, user.Id, action, requestID, mode, tc.valueField)
			attempts := 1
			if tc.allow && !tc.nonQuota {
				attempts = 2 // Explicit zero must remain idempotent, not become "absent".
			}
			for attempt := 0; attempt < attempts; attempt++ {
				response := performVerifiedManageUserRequest(t, body, identity, proof)
				require.Equal(t, http.StatusOK, response.Code)
				var result struct {
					Success *bool  `json:"success"`
					Message string `json:"message"`
				}
				require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
				require.NotNil(t, result.Success, "must receive the actual ManageUser result")
				assert.Equal(t, tc.allow, *result.Success, response.Body.String())
				if !tc.allow {
					assert.NotEmpty(t, result.Message)
				}
			}

			var stored model.User
			require.NoError(t, db.First(&stored, user.Id).Error)
			wantPaid, wantQuota := 1000, 1500
			if tc.allow && !tc.nonQuota {
				wantPaid, wantQuota = 0, 500
			}
			assert.Equal(t, wantPaid, stored.QuotaPaid)
			assert.Equal(t, 300, stored.QuotaBonus, "quota override must not erase the bonus account")
			assert.Equal(t, 200, stored.QuotaReward, "quota override must not erase the reward account")
			assert.Equal(t, wantQuota, stored.Quota)
			assert.Equal(t, common.RoleCommonUser, stored.Role)
			wantStatus := common.UserStatusDisabled
			if tc.nonQuota {
				wantStatus = common.UserStatusEnabled
			}
			assert.Equal(t, wantStatus, stored.Status, "non-quota controls must really reach their authenticated action")

			var rows []model.BalanceLedger
			require.NoError(t, db.Where("user_id = ?", user.Id).Find(&rows).Error)
			var operations, topups int64
			require.NoError(t, db.Model(&model.BalanceLedgerOperation{}).Where("user_id = ?", user.Id).Count(&operations).Error)
			require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("user_id = ? AND type = ?", user.Id, model.LogTypeTopup).Count(&topups).Error)
			if tc.allow && !tc.nonQuota {
				require.Len(t, rows, 1)
				assert.Equal(t, string(model.AccountPaid), rows[0].Account)
				assert.Equal(t, -1000, rows[0].Delta)
				assert.Equal(t, 1500, rows[0].TotalBefore)
				assert.Equal(t, 500, rows[0].TotalAfter)
				assert.Equal(t, "admin_quota:9999:"+requestID, rows[0].IdempotencyKey)
				assert.EqualValues(t, 1, operations)
				assert.EqualValues(t, 1, topups, "explicit-zero replay must not duplicate the financial log")
			} else {
				assert.Empty(t, rows)
				assert.Zero(t, operations)
				assert.Zero(t, topups)
			}
		})
	}
}
