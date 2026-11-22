package controller

import (
	"errors"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskPluginImageErrorHonorsPrivacyBoundary(t *testing.T) {
	for _, role := range []int{common.RoleCommonUser, common.RoleAdminUser, common.RoleRootUser} {
		c, recorder := newImageProtocolTestContext("")
		c.Set("role", role)
		c.Set(common.RequestIdKey, "local-image-request")
		taskErr := service.TaskErrorWrapper(errors.New(`{"code":"private-vendor-code","message":"https://private.example/api?key=secret-key quota_account=private-account","request_id":"vendor-request"}`), "fail_to_fetch_task", http.StatusBadGateway)
		respondTaskPluginImageError(c, taskErr)
		require.Equal(t, http.StatusBadGateway, recorder.Code)
		assert.Contains(t, recorder.Body.String(), "upstream request failed")
		assert.Contains(t, recorder.Body.String(), "local-image-request")
		for _, secret := range []string{"private.example", "secret-key", "private-account", "private-vendor-code", "vendor-request"} {
			assert.NotContains(t, recorder.Body.String(), secret)
		}
	}
	c, recorder := newImageProtocolTestContext("")
	common.SetContextKey(c, constant.ContextKeyChannelTest, true)
	taskErr := service.TaskErrorWrapper(errors.New(`{"code":"InvalidParameter","message":"n must be 1"}`), "fail_to_fetch_task", http.StatusBadRequest)
	respondTaskPluginImageError(c, taskErr)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "InvalidParameter")
	assert.Contains(t, recorder.Body.String(), "n must be 1")
}
