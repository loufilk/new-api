package system_setting

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

type GoogleSettings struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

var googleSettings GoogleSettings

func init() { config.GlobalConfig.Register("google", &googleSettings) }

func GetGoogleSettings() *GoogleSettings { return &googleSettings }

type GoogleConfiguration struct {
	GoogleSettings
	Enabled       bool
	ServerAddress string
}

func GoogleSettingsSnapshot() GoogleConfiguration {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	return GoogleConfiguration{GoogleSettings: googleSettings, Enabled: common.GoogleOAuthEnabled, ServerAddress: ServerAddress}
}

func (s GoogleSettings) IsConfigured() bool {
	return strings.TrimSpace(s.ClientID) != "" && strings.TrimSpace(s.ClientSecret) != ""
}
