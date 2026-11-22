package common

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGrokImageDiscoveryEndpoints(t *testing.T) {
	for _, channelType := range []int{constant.ChannelTypeOpenAI, constant.ChannelTypeXai} {
		for _, name := range []string{"grok-imagine-image", "grok-imagine-image-2.0", "grok-imagine-image-pro", "GROK-IMAGINE-IMAGE-2.0"} {
			t.Run(fmt.Sprintf("%s/channel-%d", name, channelType), func(t *testing.T) {
				endpoints := GetEndpointTypesByChannelType(channelType, name)
				require.NotEmpty(t, endpoints)
				assert.Equal(t, constant.EndpointTypeImageGeneration, endpoints[0])
				assert.Contains(t, endpoints, constant.EndpointTypeOpenAI)
			})
		}
		for _, name := range []string{"grok-imagine-video", "grok-4.6", "other-grok-imagine-image-2.0"} {
			assert.NotContains(t, GetEndpointTypesByChannelType(channelType, name), constant.EndpointTypeImageGeneration, name)
		}
	}
}
