package system_setting

import (
	"errors"
	"net/url"
	"strings"
)

// ErrPublicSiteAddressMissing is returned when a security-sensitive consumer
// needs the configured public hostname but ServerAddress is empty or invalid.
var ErrPublicSiteAddressMissing = errors.New("public site address is not configured")

// PublicSiteHostname derives the trusted public hostname from the configured
// ServerAddress. It never consults request Host headers, so callers can use it
// for security checks such as Turnstile hostname validation.
func PublicSiteHostname() (string, error) {
	address := strings.TrimSpace(ServerAddress)
	if address == "" {
		return "", ErrPublicSiteAddressMissing
	}
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", ErrPublicSiteAddressMissing
	}
	hostname := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if hostname == "" {
		return "", ErrPublicSiteAddressMissing
	}
	return hostname, nil
}

// PublicSiteDisplayHost returns the configured public hostname for user-facing
// copy. Invalid configuration yields an empty string rather than reflecting a
// request-controlled host value into email content.
func PublicSiteDisplayHost() string {
	hostname, err := PublicSiteHostname()
	if err != nil {
		return ""
	}
	return hostname
}
