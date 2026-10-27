package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderAuthEmailKeepsPlainTextAndHTMLContract(t *testing.T) {
	plainText, htmlBody := renderAuthEmail(authEmailContent{
		Heading:         "邮箱验证",
		Intro:           "您正在进行Fixture<API>邮箱验证。",
		Code:            "246810",
		ValidityLabel:   "验证码",
		ValidityMinutes: 5,
		SystemName:      "Fixture<API>",
		PublicHost:      "chuanapi.com?a=1&b=2",
	})

	assert.Contains(t, plainText, "验证码：246810")
	assert.Contains(t, plainText, "验证码 5 分钟内有效")
	assert.Contains(t, plainText, "Fixture<API>")
	assert.Contains(t, plainText, "chuanapi.com?a=1&b=2")
	assert.Contains(t, htmlBody, "246810")
	assert.Contains(t, htmlBody, "验证码 5 分钟内有效")
	assert.Contains(t, htmlBody, "Fixture&lt;API&gt;")
	assert.Contains(t, htmlBody, "chuanapi.com?a=1&amp;b=2")
	assert.NotContains(t, htmlBody, "Fixture<API>")
}

func TestRenderAuthEmailRendersEscapedActionLink(t *testing.T) {
	link := "https://console.example/user/reset?email=a&token=<code>"
	plainText, htmlBody := renderAuthEmail(authEmailContent{
		Heading:         "密码重置",
		Intro:           "您正在进行Fixture邮箱的密码重置。",
		Link:            link,
		ActionLabel:     "重置密码",
		ValidityLabel:   "重置链接<有效期>",
		ValidityMinutes: 5,
		SystemName:      "Fixture",
		PublicHost:      "console.example",
	})

	require.Contains(t, plainText, "请在浏览器中打开以下链接：\n"+link)
	assert.Contains(t, htmlBody, "重置密码")
	assert.NotContains(t, htmlBody, link, "the raw link must not be injected unescaped into HTML")
	assert.Contains(t, htmlBody, "https://console.example/user/reset?email=a&amp;token=&lt;code&gt;")
	assert.Contains(t, plainText, "重置链接<有效期> 5 分钟内有效")
	assert.Contains(t, htmlBody, "重置链接&lt;有效期&gt; 5 分钟内有效")
	assert.NotContains(t, htmlBody, "<有效期>")
}
