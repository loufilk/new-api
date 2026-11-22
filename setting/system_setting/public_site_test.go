package system_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicSiteHostnameUsesConfiguredServerAddress(t *testing.T) {
	previous := ServerAddress
	ServerAddress = "https://Example.COM:8443/path"
	t.Cleanup(func() { ServerAddress = previous })

	hostname, err := PublicSiteHostname()
	require.NoError(t, err)
	assert.Equal(t, "example.com", hostname)
	assert.Equal(t, "example.com", PublicSiteDisplayHost())
}

func TestPublicSiteHostnameFailsClosedForMissingOrInvalidAddress(t *testing.T) {
	previous := ServerAddress
	t.Cleanup(func() { ServerAddress = previous })

	for _, address := range []string{"", "example.com", "://broken"} {
		ServerAddress = address
		hostname, err := PublicSiteHostname()
		assert.Error(t, err, address)
		assert.Empty(t, hostname, address)
		assert.Empty(t, PublicSiteDisplayHost(), address)
	}
}
