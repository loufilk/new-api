package cline

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestClinePlainTextWireRemainsByteIdentical(t *testing.T) {
	body := []byte(` {"model":"deepseek/deepseek-v4.1-flash","temperature":0,"vendor":9007199254740993,"messages":[
 {"role":"system","content":"be precise"},
 {"role":"user","content":"a filename input_video is just text"},
 {"role":"assistant","content":null,"tool_calls":[{"id":"a","type":"function","function":{"name":"read","arguments":"{}"}}]},
 {"role":"tool","tool_call_id":"a","content":"plain tool result"}
 ]} `)
	got, err := normalizeClineWireContent(body)
	require.NoError(t, err)
	assert.Equal(t, body, got, "plain text must not be reconstructed by the media adapter")
}
