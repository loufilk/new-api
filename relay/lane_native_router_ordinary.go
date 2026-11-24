//go:build !lane_billing_integration_dev

package relay

import (
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

func armLaneNativeSyntheticBilling(*gin.Context, *relaycommon.RelayInfo) (bool, *types.NewAPIError) {
	return false, nil
}

func hasLaneNativeSyntheticBilling(*gin.Context) bool { return false }

func prepareLaneNativeSyntheticSelectedBilling(*gin.Context, *relaycommon.RelayInfo) (bool, *types.NewAPIError) {
	return false, nil
}

func bindLaneNativeSyntheticBilling(*gin.Context, *relaycommon.RelayInfo, channel.Adaptor, []byte) (io.Reader, *types.NewAPIError) {
	return nil, nil
}

func observeLaneNativeSyntheticTerminal(*gin.Context, *relaycommon.RelayInfo, *http.Response) *types.NewAPIError {
	return nil
}

// A constant-false ordinary capability eliminates the guarded calls.
func newAPIErrorLaneSyntheticUnsupported() *types.NewAPIError { return nil }
