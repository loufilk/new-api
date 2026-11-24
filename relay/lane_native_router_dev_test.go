//go:build lane_billing_integration_dev

package relay

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLaneNativeSyntheticStickyIntent(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{}
	require.Nil(t, relaycommon.ClaimBillingHTTPDispatch(c, info, c.Request), "ordinary nil capability is not an error")
	relaycommon.PinLaneNativeSyntheticHTTPIntent(c)
	// Deliberately lose the RelayInfo owner pointer. None of the phases can
	// treat the still-pinned request as ordinary or grant an upstream send.
	require.True(t, relaycommon.BillingHTTPDispatchOwned(c, nil))
	require.True(t, relaycommon.BillingHTTPDispatchOwned(c, info))
	assert.NotNil(t, PrepareSelectedRelayBilling(c, info))
	body, bindErr := bindLaneNativeSyntheticBilling(c, info, nil, strings.NewReader("{}"))
	assert.Nil(t, body)
	assert.NotNil(t, bindErr)
	assert.NotNil(t, relaycommon.ClaimBillingHTTPDispatch(c, nil, c.Request))
	assert.NotNil(t, relaycommon.ClaimBillingHTTPDispatch(c, info, c.Request))
	assert.NotNil(t, observeLaneNativeSyntheticTerminal(c, info, nil))
}
