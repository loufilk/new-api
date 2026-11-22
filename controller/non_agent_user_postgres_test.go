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

func TestRetiredQualificationCannotBeCreatedOrRestored(t *testing.T) {
	_, identity, target := setupAdminUserTest(t)
	response := adminUserRequest(http.MethodPost, "/api/user/", `{"username":"retired-role","password":"synthetic-local-password","role":5}`, "", identity, common.RoleRootUser, nil, CreateUser)
	assert.Equal(t, http.StatusBadRequest, response.Code)
	var created int64
	require.NoError(t, model.DB.Model(&model.User{}).Where("username = ?", "retired-role").Count(&created).Error)
	assert.Zero(t, created)
	for _, context := range [][]byte{nil, []byte(`{"action":"purchase.confirm","request_id":"synthetic-retired"}`)} {
		_, err := service.BindVerificationOperation(service.VerificationOperation{Scope: "agent.transaction", Context: context})
		assert.ErrorIs(t, err, service.ErrProofScope, "removed financial scopes cannot mint a proof")
	}
	for _, action := range []string{"set_agent", "unset_agent"} {
		response = adminUserRequest(http.MethodPost, "/api/user/manage", fmt.Sprintf(`{"id":%d,"action":%q}`, target.Id, action), "", identity, common.RoleRootUser, nil, ManageUser)
		assert.Contains(t, response.Body.String(), `"success":false`)
		_, err := service.BindVerificationOperation(service.VerificationOperation{Scope: service.VerificationScopeAdminUserManage, Context: []byte(fmt.Sprintf(`{"user_id":%d,"action":%q}`, target.Id, action))})
		assert.Error(t, err, "retired operations cannot obtain security proofs")
	}
}

func TestSelfDTONormalizesLegacyRoleWithoutChangingWalletsOrCredentials(t *testing.T) {
	_, _, user := setupAdminUserTest(t)
	user.Role, user.QuotaPaid, user.Quota = common.RoleAgentUser, 100, 100
	require.NoError(t, model.DB.Save(user).Error)
	for _, role := range []int{common.RoleCommonUser, common.RoleAgentUser, common.RoleAdminUser} {
		data := buildSelfUserDataWithRole(user, min(role, user.Role))
		assert.Equal(t, common.RoleCommonUser, data["role"])
		assert.Equal(t, user.Quota, data["quota"])
		assert.Contains(t, data, "agent_profile")
		assert.Nil(t, data["agent_profile"], "a legacy numeric role must not create an A07 business profile")
		assert.NotContains(t, data, "password")
		permissions, ok := data["permissions"].(map[string]any)
		require.True(t, ok)
		for _, name := range []string{"agent_history", "agent_sales", "agent_settlement", "agent_admin", "agent_redemption"} {
			assert.Contains(t, permissions, name)
			assert.Equal(t, false, permissions[name], "legacy qualification must not restore %s", name)
		}
	}
	var after model.User
	require.NoError(t, model.DB.First(&after, user.Id).Error)
	assert.Equal(t, *user, after)
}
