package monitor

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

const settingsOption = "ChuanapiMonitorConfig"

var settingsWriteMu sync.Mutex
var targetIDPattern = regexp.MustCompile(`^route-[a-f0-9]{32}$`)

type Config struct {
	Enabled      bool              `json:"enabled"`
	Title        string            `json:"title"`
	Description  string            `json:"description"`
	Groups       map[string]string `json:"groups"`
	HiddenGroups map[string]bool   `json:"hidden_groups"`
}

func readConfig() (Config, error) {
	common.OptionMapRWMutex.RLock()
	raw := common.OptionMap[settingsOption]
	common.OptionMapRWMutex.RUnlock()
	config := Config{Enabled: true, Groups: map[string]string{}, HiddenGroups: map[string]bool{}}
	if raw == "" {
		return config, nil
	}
	if err := common.UnmarshalJsonStr(raw, &config); err != nil {
		return Config{}, errUnavailable
	}
	if err := config.normalize(); err != nil {
		return Config{}, errUnavailable
	}
	return config, nil
}

func (config *Config) normalize() error {
	config.Title = strings.TrimSpace(config.Title)
	config.Description = strings.TrimSpace(config.Description)
	if utf8.RuneCountInString(config.Title) > 120 || utf8.RuneCountInString(config.Description) > 500 || len(config.Groups) > 128 || len(config.HiddenGroups) > 128 {
		return errors.New("monitor.settings.invalid")
	}
	for id, name := range config.Groups {
		name = strings.TrimSpace(name)
		if !targetIDPattern.MatchString(id) || utf8.RuneCountInString(name) > 80 || strings.ContainsFunc(name, unicode.IsControl) {
			return errors.New("monitor.settings.invalid")
		}
		if name == "" {
			delete(config.Groups, id)
		} else {
			config.Groups[id] = name
		}
	}
	for id, hidden := range config.HiddenGroups {
		if !targetIDPattern.MatchString(id) {
			return errors.New("monitor.settings.invalid")
		}
		if !hidden {
			delete(config.HiddenGroups, id)
		}
	}
	if config.HiddenGroups == nil {
		config.HiddenGroups = map[string]bool{}
	}
	if config.Groups == nil {
		config.Groups = map[string]string{}
	}
	return nil
}

// These handlers must only be mounted under the host's RootOptions scope.
func (s *Service) RegisterSettings(group *gin.RouterGroup) {
	group.GET("/model-monitor", s.getSettings)
	group.PUT("/model-monitor", s.saveSettings)
	group.GET("/model-monitor/targets", s.getSettingsTargets)
}

func (s *Service) getSettings(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	config, err := s.config()
	if err != nil {
		unavailable(c)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"config": config, "environment_enabled": s.enabled}})
}

func (s *Service) saveSettings(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var request struct {
		Enabled      *bool             `json:"enabled"`
		Title        *string           `json:"title"`
		Description  *string           `json:"description"`
		Groups       map[string]string `json:"groups"`
		HiddenGroups map[string]bool   `json:"hidden_groups"`
	}
	if err := common.DecodeJson(c.Request.Body, &request); err != nil || request.Enabled == nil || request.Title == nil || request.Description == nil || request.Groups == nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "monitor.settings.invalid"})
		return
	}
	config := Config{Enabled: *request.Enabled, Title: *request.Title, Description: *request.Description, Groups: request.Groups, HiddenGroups: request.HiddenGroups}
	if err := config.normalize(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	settingsWriteMu.Lock()
	defer settingsWriteMu.Unlock()
	// Older clients omit visibility settings; preserve the current hidden groups.
	if request.HiddenGroups == nil {
		current, err := s.config()
		if err != nil {
			unavailable(c)
			return
		}
		config.HiddenGroups = current.HiddenGroups
	}
	encoded, err := common.Marshal(config)
	if err != nil {
		unavailable(c)
		return
	}
	err = model.UpdateOptionsBulk(map[string]string{settingsOption: string(encoded)})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "monitor.settings.saveFailed"})
		return
	}
	c.JSON(http.StatusOK, envelope[Config]{Success: true, Data: config})
}

func (s *Service) getSettingsTargets(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	// Administrators can name groups while public access is off. The environment
	// kill switch still blocks every upstream read, including this catalog.
	data, err := s.snapshotWithConfig(c.Request.Context(), Config{Enabled: true})
	if err != nil {
		unavailable(c)
		return
	}
	c.JSON(http.StatusOK, envelope[[]Target]{Success: true, Data: data.Targets})
}
