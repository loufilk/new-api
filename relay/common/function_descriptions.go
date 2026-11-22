package common

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ensureFunctionDescriptions is an opt-in channel parameter operation for
// upstreams that require a non-empty description on function definitions.
// It covers Chat tools, legacy functions, and Responses tools without adding
// unsupported fields to hosted/custom tools. Existing non-empty strings and
// invalid non-string values are left alone; only missing/null/"" are filled.
// Namespace contents and message-local dynamic tool declarations are not
// rewritten. The caller chooses the fallback; there is no global default.
//
// A non-empty fallback also survives the Chat-to-Responses conversion, which
// drops empty descriptions. Do not rerun other, potentially non-idempotent,
// channel operations after conversion to compensate for that behavior.
func ensureFunctionDescriptions(data []byte, value any) ([]byte, error) {
	fallback, ok := value.(string)
	if !ok || strings.TrimSpace(fallback) == "" {
		return nil, fmt.Errorf("function description fallback must be a non-empty string")
	}
	root := gjson.ParseBytes(data)
	if !gjson.ValidBytes(data) || !root.IsObject() {
		return nil, fmt.Errorf("function description compatibility requires a valid JSON object")
	}

	var definitionPaths []string
	var tools []gjson.Result
	if rawTools := root.Get("tools"); rawTools.IsArray() {
		tools = rawTools.Array()
	}
	for index, tool := range tools {
		if !tool.IsObject() {
			continue
		}
		toolType := tool.Get("type")
		path := "tools." + strconv.Itoa(index)
		if function := tool.Get("function"); function.IsObject() {
			// The existing Chat converter also accepts an omitted tool type.
			if !toolType.Exists() || toolType.Type == gjson.String && (toolType.Str == "function" || toolType.Str == "") {
				definitionPaths = append(definitionPaths, path+".function")
			}
			continue
		}
		if toolType.Type == gjson.String && toolType.Str == "function" && !tool.Get("function").Exists() {
			definitionPaths = append(definitionPaths, path)
		}
	}
	if functions := root.Get("functions"); functions.IsArray() {
		for index := range functions.Array() {
			definitionPaths = append(definitionPaths, "functions."+strconv.Itoa(index))
		}
	}

	result := data
	for _, path := range definitionPaths {
		definition := gjson.GetBytes(result, path)
		name := definition.Get("name")
		if !definition.IsObject() || name.Type != gjson.String || strings.TrimSpace(name.Str) == "" {
			continue
		}
		description := definition.Get("description")
		if description.Exists() && description.Type != gjson.Null && (description.Type != gjson.String || description.Str != "") {
			continue
		}
		var err error
		result, err = sjson.SetBytes(result, path+".description", fallback)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}
