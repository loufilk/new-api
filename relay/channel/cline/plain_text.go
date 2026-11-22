package cline

import (
	"github.com/tidwall/gjson"
	"slices"
)

// clinePlainTextWire avoids rebuilding canonical text/tool-call histories. It
// never bypasses validation of a media part or the required empty-text repair.
func clinePlainTextWire(messages gjson.Result) bool {
	if !messages.IsArray() || len(messages.Array()) == 0 {
		return false
	}
	for _, message := range messages.Array() {
		if !message.IsObject() {
			return false
		}
		role := message.Get("role")
		if role.Type != gjson.String || !slices.Contains([]string{"user", "assistant", "system", "developer", "tool"}, role.Str) {
			return false
		}
		content := message.Get("content")
		if role.Str == "assistant" && (!content.Exists() || content.Type == gjson.Null) {
			continue
		}
		if content.Type != gjson.String || clineRequiresContent(role.Str) && content.Str == "" {
			return false
		}
	}
	return true
}
