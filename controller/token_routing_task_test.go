package controller

import (
	"net/http"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenNoRetryNativeLockedTaskWithoutPinRefundsOnce(t *testing.T) {
	if os.Getenv("TEST_TASK_DB_DIALECT") != "postgres" {
		t.Skip("requires isolated PostgreSQL test wrapper")
	}
	configureTokenAutoGroupsTest(t, "5", `["default","vip"]`)
	f := newTaskSubmissionFinancialFixture(t, "native-locked-no-retry", 1000, 100)
	common.SetContextKey(f.c, constant.ContextKeyTokenNoRetryWithinGroup, true)
	common.SetContextKey(f.c, constant.ContextKeyTokenGroup, "auto")
	common.SetContextKey(f.c, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(f.c, constant.ContextKeyUsingGroup, "default")
	common.SetContextKey(f.c, constant.ContextKeyAutoGroup, "default")
	common.SetContextKey(f.c, constant.ContextKeyTokenAutoGroups, []string{"default", "vip"})
	f.info.TokenGroup = "auto"
	_, pinned, _ := service.GetChannelConstraints(f.c).ResolvedPin()
	require.False(t, pinned, "native ResolveOriginTask sets LockedChannel without a constraint pin")
	require.NotNil(t, f.info.LockedChannel)
	// A real alternative remains selectable, so absence of a candidate cannot
	// accidentally make this regression pass instead of the locked-channel cap.
	seedTokenModelsAbility(t, f.db, "vip", "plugin-model", constant.ChannelTypeTaskPlugin, true)
	calls := 0
	outcome, taskErr := executeTaskSubmissionWith(f.c, f.info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		calls++
		return nil, &dto.TaskError{Code: "synthetic_failure", Message: "synthetic unavailable", StatusCode: http.StatusServiceUnavailable}
	})
	require.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "synthetic_failure", taskErr.Code)
	assert.Equal(t, 1, calls)
	f.assertFinancial(t, model.BillingRefunded, 0, 0, 0, 2)
	f.assertTaskCount(t, 0)
	assert.False(t, f.c.Writer.Written())
}
