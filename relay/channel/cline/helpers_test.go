package cline

import "github.com/tidwall/gjson"

func gjsonHas(payload []byte, path string) bool {
	return gjson.GetBytes(payload, path).Exists()
}

func gjsonString(payload []byte, path string) string {
	return gjson.GetBytes(payload, path).String()
}

func gjsonInt(payload []byte, path string) int64 {
	return gjson.GetBytes(payload, path).Int()
}
