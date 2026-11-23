//go:build enterprise_lane_lab

package controller

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const enterpriseLaneTestChat = `{"model":"gpt-4o","messages":[{"role":"user","content":"synthetic input"}],"max_tokens":20,"stream":true,"stream_options":{"include_usage":true}}`

func TestEnterpriseLaneFixtureWireAndFrozenQuota(t *testing.T) {
	chat, wire, err := enterpriseLaneWireBody([]byte(enterpriseLaneTestChat), "gpt-4o")
	require.NoError(t, err)
	assert.JSONEq(t, enterpriseLaneTestChat, string(wire))
	price := enterpriseLaneRate{prompt: decimal.New(1, -1), completion: decimal.New(2, -1)}
	cap, err := price.quota(len(wire), int(*chat.MaxTokens), true)
	require.NoError(t, err)
	actual, err := price.quota(80, 20, false)
	require.NoError(t, err)
	assert.Equal(t, 12, actual)
	assert.Greater(t, cap, actual)

	for _, invalid := range []string{
		strings.Replace(enterpriseLaneTestChat, `"max_tokens":20`, `"max_tokens":20,"max_tokens":40`, 1),
		strings.Replace(enterpriseLaneTestChat, `"max_tokens":20`, `"max_tokens":18446744073709551615`, 1),
		strings.Replace(enterpriseLaneTestChat, `"max_tokens":20`, `"max_completion_tokens":20`, 1),
		strings.Replace(enterpriseLaneTestChat, `"stream":true`, `"stream":false`, 1),
		strings.Replace(enterpriseLaneTestChat, `"include_usage":true`, `"include_usage":false`, 1),
		strings.Replace(enterpriseLaneTestChat, `"stream":true`, `"stream":true,"n":2`, 1),
		strings.Replace(enterpriseLaneTestChat, `"stream":true`, `"stream":true,"tools":[]`, 1),
		strings.Replace(enterpriseLaneTestChat, `"role":"user"`, `"role":"tool"`, 1),
		enterpriseLaneTestChat + `{}`,
	} {
		_, _, err := enterpriseLaneWireBody([]byte(invalid), "gpt-4o")
		assert.Error(t, err, "%s", invalid)
	}
}

func TestEnterpriseLaneFixtureOnlyLiteralLoopback(t *testing.T) {
	for addr, expect := range map[string]string{
		"http://127.0.0.1:3456":  "http://127.0.0.1:3456/v1/chat/completions",
		"http://[::1]:3456/":     "http://[::1]:3456/v1/chat/completions",
		"http://localhost:3456":  "",
		"http://0.0.0.0:3456":    "",
		"http://127.0.0.1:0":     "",
		"http://127.0.0.1/path":  "",
		"http://a@127.0.0.1:34":  "",
		"http://127.0.0.1:34?x":  "",
		"http://127.0.0.1:34#x":  "",
		"https://127.0.0.1:3456": "",
	} {
		actual, err := enterpriseLaneUpstream(addr)
		if expect == "" {
			assert.Error(t, err, "%s", addr)
		} else {
			assert.NoError(t, err, "%s", addr)
		}
		assert.Equal(t, expect, actual)
	}
}

func TestEnterpriseLaneFixtureStreamWithholdsUnprovenTerminal(t *testing.T) {
	const item = `{"id":"fixture","object":"chat.completion.chunk","model":"gpt-4o","choices":`
	content := "data: " + item + `[{"index":0,"delta":{"content":"one"},"finish_reason":null}]}` + "\n\n"
	finish := "data: " + item + `[{"index":0,"delta":{"content":"final"},"finish_reason":"stop"}]}` + "\n\n"
	usage := "data: " + item + `[],"usage":{"prompt_tokens":80,"completion_tokens":20,"total_tokens":100}}` + "\n\n"
	done := "data: [DONE]\n\n"
	good := content + finish + usage + done
	for _, tt := range []struct {
		name string
		body string
		ok   bool
	}{
		{"clean", good, true},
		{"eof after receipt", content + finish + usage, false},
		{"unterminated done", strings.TrimSuffix(good, "\n"), false},
		{"data after done", good + usage, false},
		{"early usage", content + usage + finish + done, false},
		{"duplicate usage", content + finish + usage + usage + done, false},
		{"duplicate billing field", strings.Replace(good, `"prompt_tokens":80`, `"prompt_tokens":80,"prompt_tokens":79`, 1), false},
		{"cached details unsupported", strings.Replace(good, `"prompt_tokens":80`, `"prompt_tokens":80,"prompt_tokens_details":{"cached_tokens":30}`, 1), false},
		{"wrong aggregate", strings.Replace(good, `"total_tokens":100`, `"total_tokens":99`, 1), false},
		{"null aggregate", strings.Replace(good, `"total_tokens":100`, `"total_tokens":null`, 1), false},
		{"completion above cap", strings.Replace(strings.Replace(good, `"completion_tokens":20`, `"completion_tokens":21`, 1), `"total_tokens":100`, `"total_tokens":101`, 1), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var ordinary []string
			terminal, err := readEnterpriseLaneStream(strings.NewReader(tt.body), "gpt-4o", 160, 20, func(data []byte) { ordinary = append(ordinary, string(data)) })
			require.Len(t, ordinary, 1)
			assert.Contains(t, ordinary[0], `"content":"one"`)
			assert.NotContains(t, ordinary[0], `"content":"final"`)
			if !tt.ok {
				require.Error(t, err)
				assert.Nil(t, terminal.finishFrame)
				assert.Nil(t, terminal.usageFrame)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, 80, terminal.prompt)
			assert.Equal(t, 20, terminal.completion)
			assert.Contains(t, string(terminal.finishFrame), `"finish_reason":"stop"`)
			assert.Contains(t, string(terminal.usageFrame), `"total_tokens":100`)
		})
	}
}
