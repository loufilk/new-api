package middleware

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
)

const (
	turnstileMaxTokenLength   = 2048
	turnstileMaxResponseBytes = 64 * 1024
)

var turnstileHTTPClient = &http.Client{Timeout: 10 * time.Second}

type turnstileCheckResponse struct {
	Success    bool     `json:"success"`
	Hostname   string   `json:"hostname"`
	Action     string   `json:"action"`
	ErrorCodes []string `json:"error-codes"`
}

// turnstileActions maps a registered Gin route pattern to the widget action its
// frontend is expected to render. Keys are Gin registration patterns so wildcard
// and alias routes resolve to the same action. Routes outside this table fail
// closed when TurnstileCheck is called without an explicit action.
var turnstileActions = map[string]string{
	"GET /api/verification":   "signup",
	"POST /api/user/register": "signup",
	"POST /api/user/login":    "login",
	"GET /api/reset_password": "reset_password",
	"POST /api/user/checkin":  "checkin",
}

func turnstileActionForRoute(method, fullPath string) string {
	return turnstileActions[method+" "+fullPath]
}

// TurnstileCheck validates the Siteverify response against the route's fixed
// action and the configured public hostname. Callers may still pass an explicit
// action; without one the action is resolved from the registered Gin route and
// an unmapped route is rejected before any Siteverify request is sent.
func TurnstileCheck(expectedAction ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !common.TurnstileCheckEnabled {
			c.Next()
			return
		}

		action := ""
		if len(expectedAction) > 0 {
			action = expectedAction[0]
		} else {
			action = turnstileActionForRoute(c.Request.Method, c.FullPath())
		}

		response := strings.TrimSpace(c.Query("turnstile"))
		if response == "" || len(response) > turnstileMaxTokenLength {
			turnstileReject(c)
			return
		}
		if strings.TrimSpace(common.TurnstileSecretKey) == "" || action == "" {
			common.SysLog("Turnstile verification rejected: incomplete server configuration")
			turnstileReject(c)
			return
		}
		expectedHostname, hostnameErr := system_setting.PublicSiteHostname()
		if hostnameErr != nil {
			common.SysLog("Turnstile verification rejected: public hostname is not configured")
			turnstileReject(c)
			return
		}

		form := url.Values{
			"secret":   {common.TurnstileSecretKey},
			"response": {response},
			"remoteip": {c.ClientIP()},
		}
		request, err := http.NewRequestWithContext(
			c.Request.Context(),
			http.MethodPost,
			"https://challenges.cloudflare.com/turnstile/v0/siteverify",
			strings.NewReader(form.Encode()),
		)
		if err != nil {
			common.SysLog("Turnstile verification request could not be created")
			turnstileReject(c)
			return
		}
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		rawRes, err := turnstileHTTPClient.Do(request)
		if err != nil {
			common.SysLog(fmt.Sprintf("Turnstile Siteverify request failed: %v", err))
			turnstileReject(c)
			return
		}
		defer rawRes.Body.Close()

		if rawRes.StatusCode < http.StatusOK || rawRes.StatusCode >= http.StatusMultipleChoices {
			common.SysLog(fmt.Sprintf("Turnstile Siteverify returned HTTP %d", rawRes.StatusCode))
			turnstileReject(c)
			return
		}

		var result turnstileCheckResponse
		if err = common.DecodeJson(io.LimitReader(rawRes.Body, turnstileMaxResponseBytes), &result); err != nil {
			common.SysLog(fmt.Sprintf("Turnstile Siteverify returned an invalid response: %v", err))
			turnstileReject(c)
			return
		}
		if !result.Success || result.Action != action || !strings.EqualFold(result.Hostname, expectedHostname) {
			common.SysLog(fmt.Sprintf(
				"Turnstile verification rejected: success=%t action=%q hostname=%q errors=%v",
				result.Success,
				result.Action,
				result.Hostname,
				result.ErrorCodes,
			))
			turnstileReject(c)
			return
		}

		c.Next()
	}
}

func turnstileReject(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
		"success": false,
		"code":    "TURNSTILE_VERIFICATION_FAILED",
		"message": "Turnstile 校验失败，请刷新后重试！",
	})
}
