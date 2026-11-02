package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTokenAutoGroupsContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	return ctx
}

func TestSetupContextForTokenPreservesCustomAutoGroupsOrder(t *testing.T) {
	ctx := newTokenAutoGroupsContext()
	token := &model.Token{Id: 1, UserId: 2, AutoGroups: `["vip","default"]`}

	require.NoError(t, SetupContextForToken(ctx, token))
	value, ok := common.GetContextKey(ctx, constant.ContextKeyTokenAutoGroups)
	require.True(t, ok)
	assert.Equal(t, []string{"vip", "default"}, value)
}

func TestSetupContextForTokenTreatsStoredEmptyArrayAsInheritance(t *testing.T) {
	ctx := newTokenAutoGroupsContext()
	token := &model.Token{Id: 1, UserId: 2, AutoGroups: `[]`}

	require.NoError(t, SetupContextForToken(ctx, token))
	_, ok := common.GetContextKey(ctx, constant.ContextKeyTokenAutoGroups)
	assert.False(t, ok)
}

func TestSetupContextForTokenMalformedAutoGroupsFailsClosed(t *testing.T) {
	ctx := newTokenAutoGroupsContext()
	token := &model.Token{Id: 1, UserId: 2, AutoGroups: `not-json`}

	require.NoError(t, SetupContextForToken(ctx, token))
	value, ok := common.GetContextKey(ctx, constant.ContextKeyTokenAutoGroups)
	require.True(t, ok)
	assert.Equal(t, []string{}, value)
}

func TestSetupContextForSmartTokenKeepsModelLimitsAndManualDraft(t *testing.T) {
	ctx := newTokenAutoGroupsContext()
	token := &model.Token{
		Id: 1, UserId: 2, Group: "old-group", AutoGroups: `["old-group"]`,
		SmartRouting: true, RoutingStrategy: "price", NoRetryWithinGroup: true,
		ModelLimitsEnabled: true, ModelLimits: "permitted-model",
	}
	require.NoError(t, SetupContextForToken(ctx, token))
	assert.Equal(t, "auto", common.GetContextKeyString(ctx, constant.ContextKeyTokenGroup))
	assert.True(t, common.GetContextKeyBool(ctx, constant.ContextKeyTokenSmartRouting))
	assert.Equal(t, "price", common.GetContextKeyString(ctx, constant.ContextKeyTokenRoutingStrategy))
	assert.True(t, common.GetContextKeyBool(ctx, constant.ContextKeyTokenNoRetryWithinGroup))
	assert.True(t, common.GetContextKeyBool(ctx, constant.ContextKeyTokenModelLimitEnabled))
	assert.Equal(t, "old-group", token.Group, "internal virtual dispatch must not rewrite the stored draft")
	assert.Equal(t, `["old-group"]`, token.AutoGroups)
}
