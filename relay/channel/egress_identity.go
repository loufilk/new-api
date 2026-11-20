package channel

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/http/httpguts"
)

// channelOrigin is the intended credential recipient, not the proxy peer.
type channelOrigin struct {
	scheme    string
	authority string
}

func channelEgressOrigin(target *url.URL, webSocket bool) (channelOrigin, error) {
	if target == nil || target.Opaque != "" || target.User != nil || target.Fragment != "" || target.RawFragment != "" {
		return channelOrigin{}, errors.New("invalid upstream relay URL")
	}
	var defaultPort string
	switch target.Scheme {
	case "http", "https":
		if webSocket {
			return channelOrigin{}, errors.New("invalid upstream WebSocket scheme")
		}
	case "ws", "wss":
		if !webSocket {
			return channelOrigin{}, errors.New("invalid upstream HTTP scheme")
		}
	default:
		return channelOrigin{}, errors.New("invalid upstream relay scheme")
	}
	if target.Scheme == "https" || target.Scheme == "wss" {
		defaultPort = "443"
	} else {
		defaultPort = "80"
	}
	wireAuthority, err := httpguts.PunycodeHostPort(target.Host)
	if err != nil || !httpguts.ValidHostHeader(wireAuthority) || strings.HasSuffix(wireAuthority, ":") {
		return channelOrigin{}, errors.New("invalid upstream relay authority")
	}
	parsed := &url.URL{Host: wireAuthority}
	host, port := parsed.Hostname(), parsed.Port()
	if strings.HasPrefix(wireAuthority, "[") {
		ip, err := netip.ParseAddr(host)
		if err != nil || !ip.Is6() {
			return channelOrigin{}, errors.New("invalid upstream relay IP address")
		}
		// Canonicalize IPv6 spelling without changing the optional zone.
		host = ip.String()
	} else {
		if host == "" || strings.ContainsAny(host, ":[]%") {
			return channelOrigin{}, errors.New("invalid upstream relay authority")
		}
		host = strings.ToLower(host)
	}
	if port != "" {
		n, err := strconv.ParseUint(port, 10, 16)
		if err != nil || n == 0 {
			return channelOrigin{}, errors.New("invalid upstream relay port")
		}
		port = strconv.FormatUint(n, 10)
	} else {
		port = defaultPort
	}
	return channelOrigin{scheme: target.Scheme, authority: net.JoinHostPort(host, port)}, nil
}

// check verifies the credential recipient without changing URL or headers.
// An alternate Host is not authorized merely because the socket URL matches.
func (origin channelOrigin) check(target *url.URL, requestHost string, header http.Header) error {
	webSocket := origin.scheme == "ws" || origin.scheme == "wss"
	actual, err := channelEgressOrigin(target, webSocket)
	if err != nil {
		return err
	}
	if actual != origin {
		return errors.New("upstream credential recipient changed while preparing the request")
	}
	if requestHost != "" && !origin.matchesHost(target, requestHost, webSocket) {
		return errors.New("upstream Host does not match the relay URL")
	}
	hostHeaders := 0
	for name, values := range header {
		switch {
		case strings.EqualFold(name, "Host"):
			hostHeaders++
			if hostHeaders != 1 || len(values) != 1 || !origin.matchesHost(target, values[0], webSocket) {
				return errors.New("upstream Host header does not match the relay URL")
			}
		case strings.EqualFold(name, "Proxy-Authorization"):
			return errors.New("upstream proxy authentication must be set on the configured proxy URL")
		}
	}
	return nil
}

func (origin channelOrigin) matchesHost(target *url.URL, host string, webSocket bool) bool {
	if host == target.Host {
		return true
	}
	// A Host value contains only the authority, not a URL reference.
	if host == "" || strings.ContainsAny(host, "/?#@") {
		return false
	}
	parsed, err := url.Parse(origin.scheme + "://" + host)
	if err != nil {
		return false
	}
	headerOrigin, err := channelEgressOrigin(parsed, webSocket)
	return err == nil && headerOrigin == origin
}
