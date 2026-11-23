//go:build enterprise_lane_lab

package controller

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/tidwall/gjson"
)

// A provider receipt with duplicate billing fields is ambiguous. Require
// unambiguous ASCII-style object names, including inside the usage receipt.
func enterpriseLaneUniqueJSON(data []byte) bool {
	if !gjson.ValidBytes(data) {
		return false
	}
	var unique func(gjson.Result, int) bool
	unique = func(value gjson.Result, depth int) bool {
		if depth > 8 {
			return false
		}
		if !value.IsObject() && !value.IsArray() {
			return true
		}
		seen := make(map[string]struct{})
		valid := true
		value.ForEach(func(key, child gjson.Result) bool {
			if value.IsObject() {
				name := key.String()
				for _, r := range name {
					if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
						valid = false
						return false
					}
				}
				if _, exists := seen[name]; name == "" || exists {
					valid = false
					return false
				}
				seen[name] = struct{}{}
			}
			valid = unique(child, depth+1)
			return valid
		})
		return valid
	}
	root := gjson.ParseBytes(data)
	return root.IsObject() && unique(root, 0)
}

type enterpriseLaneChunk struct {
	ID                string `json:"id"`
	Model             string `json:"model"`
	Object            string `json:"object"`
	Created           int64  `json:"created"`
	SystemFingerprint string `json:"system_fingerprint,omitempty"`
	Choices           []struct {
		Index *int `json:"index"`
		Delta *struct {
			Role    *string `json:"role"`
			Content *string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		Prompt     *int `json:"prompt_tokens"`
		Completion *int `json:"completion_tokens"`
		Total      *int `json:"total_tokens"`
	} `json:"usage"`
}

type enterpriseLaneTerminal struct {
	prompt, completion      int
	finishFrame, usageFrame []byte
}

// readEnterpriseLaneStream preserves early content streaming but returns the
// native finish/usage frames only after exactly one compatible usage receipt,
// a complete [DONE] event, and a clean fixture EOF. This intentionally refuses
// real-provider extensions, detail/cached usage, and locally estimated tokens.
func readEnterpriseLaneStream(reader io.Reader, model string, promptCap, completionCap int, emitContent func([]byte)) (enterpriseLaneTerminal, error) {
	var terminal enterpriseLaneTerminal
	invalid := errors.New("unconfirmed synthetic provider stream")
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), enterpriseLaneBodyMax)
	var frame []byte
	var id string
	var content, done bool
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) != 0 {
			if !done && line[0] == ':' && len(frame) == 0 {
				continue
			}
			if done || len(frame) != 0 || !bytes.HasPrefix(line, []byte("data:")) {
				return enterpriseLaneTerminal{}, invalid
			}
			data := bytes.TrimSpace(line[len("data:"):])
			if len(data) == 0 {
				return enterpriseLaneTerminal{}, invalid
			}
			frame = bytes.Clone(data)
			continue
		}
		if len(frame) == 0 {
			continue
		}
		data := frame
		frame = nil
		if bytes.Equal(data, []byte("[DONE]")) {
			if terminal.usageFrame == nil || !content {
				return enterpriseLaneTerminal{}, invalid
			}
			done = true
			continue
		}
		var chunk enterpriseLaneChunk
		if !utf8.Valid(data) || !enterpriseLaneUniqueJSON(data) || common.DecodeJsonStrict(bytes.NewReader(data), &chunk) != nil ||
			chunk.ID == "" || len(chunk.ID) > 128 || chunk.Model != model || chunk.Object != "chat.completion.chunk" || chunk.Choices == nil {
			return enterpriseLaneTerminal{}, invalid
		}
		if id == "" {
			id = chunk.ID
		}
		if chunk.ID != id || terminal.usageFrame != nil {
			return enterpriseLaneTerminal{}, invalid
		}
		if u := chunk.Usage; u != nil {
			if len(chunk.Choices) != 0 || terminal.finishFrame == nil || u.Prompt == nil || u.Completion == nil || u.Total == nil ||
				*u.Prompt <= 0 || *u.Prompt > promptCap || *u.Completion <= 0 || *u.Completion > completionCap || *u.Total != *u.Prompt+*u.Completion {
				return enterpriseLaneTerminal{}, invalid
			}
			terminal.prompt, terminal.completion, terminal.usageFrame = *u.Prompt, *u.Completion, data
			continue
		}
		if terminal.finishFrame != nil || len(chunk.Choices) != 1 || chunk.Choices[0].Index == nil || *chunk.Choices[0].Index != 0 || chunk.Choices[0].Delta == nil {
			return enterpriseLaneTerminal{}, invalid
		}
		choice := chunk.Choices[0]
		if choice.Delta.Role != nil && *choice.Delta.Role != "assistant" {
			return enterpriseLaneTerminal{}, invalid
		}
		if choice.Delta.Content != nil && strings.TrimSpace(*choice.Delta.Content) != "" {
			content = true
		}
		if choice.FinishReason != nil {
			if *choice.FinishReason != "stop" && *choice.FinishReason != "length" {
				return enterpriseLaneTerminal{}, invalid
			}
			terminal.finishFrame = data
			continue
		}
		emitContent(data)
	}
	if scanner.Err() != nil || !done || len(frame) != 0 {
		return enterpriseLaneTerminal{}, invalid
	}
	return terminal, nil
}
