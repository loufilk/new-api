package setting

import (
	"encoding/json"

	"github.com/QuantumNous/new-api/common"
)

// Chats remains the legacy wire option for administrators who already saved it.
// The application no longer provides built-in third-party presets.
var Chats = []map[string]string{}

func UpdateChatsByJsonString(jsonString string) error {
	Chats = make([]map[string]string, 0)
	return json.Unmarshal([]byte(jsonString), &Chats)
}

func Chats2JsonString() string {
	jsonBytes, err := json.Marshal(Chats)
	if err != nil {
		common.SysLog(common.LogText("error marshalling chats: %s", err.Error()))
		return "[]"
	}
	return string(jsonBytes)
}
