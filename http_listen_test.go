package main

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHTTPListenAddress(t *testing.T) {
	for _, tc := range []struct{ name, override, host, expected string }{
		{"existing wildcard default", "", "", ":3000"},
		{"IPv4 loopback", "127.0.0.1", "", "127.0.0.1:3000"},
		{"IPv6 loopback", "::1", "", "[::1]:3000"},
		{"upstream HOST fallback", "", "127.0.0.2", "127.0.0.2:3000"},
		{"existing explicit address wins", "127.0.0.1", "127.0.0.2", "127.0.0.1:3000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HTTP_BIND_ADDRESS", tc.override)
			t.Setenv("HOST", tc.host)
			assert.Equal(t, tc.expected, httpListenAddress("3000"))
		})
	}
}

func TestHTTPListenAddressRejectsInvalidPort(t *testing.T) {
	t.Setenv("HTTP_BIND_ADDRESS", "127.0.0.1")
	listener, err := net.Listen("tcp", httpListenAddress("invalid:port"))
	if listener != nil {
		t.Cleanup(func() { _ = listener.Close() })
	}
	assert.Error(t, err)
	assert.Nil(t, listener, "invalid startup address must not open a listener")
}
