package billingexpr

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
	"github.com/tidwall/gjson"
)

var sensitiveBillingHeaders = map[string]struct{}{
	"authorization":       {},
	"proxy-authorization": {},
	"cookie":              {},
	"set-cookie":          {},
	"x-api-key":           {},
	"api-key":             {},
	"x-security-proof":    {},
}

const (
	maxPersistedBillingParamPathBytes   = 128
	maxPersistedBillingParamStringBytes = 256
	maxPersistedBillingParamDepth       = 8
)

// Credential-bearing names are rejected at any path depth. Content-bearing
// names are rejected when they are the selected leaf. Async task billing only
// needs compact pricing switches/measurements; user content belongs in usage
// facts or the original request lifecycle, not in a persisted billing snapshot.
var sensitiveBillingParamSegments = map[string]struct{}{
	"apikey": {}, "xapikey": {}, "authorization": {}, "proxyauthorization": {}, "auth": {},
	"cookie": {}, "setcookie": {}, "credential": {}, "credentials": {}, "password": {},
	"passwd": {}, "secret": {}, "clientsecret": {}, "accesskey": {}, "accesskeyid": {},
	"secretkey": {}, "accesstoken": {}, "refreshtoken": {}, "idtoken": {}, "token": {},
	"jwt": {}, "privatekey": {}, "securityproof": {}, "xsecurityproof": {}, "session": {}, "sessionid": {},
}

var contentBillingParamLeaves = map[string]struct{}{
	"body": {}, "content": {}, "email": {}, "file": {}, "files": {}, "image": {}, "images": {},
	"input": {}, "instructions": {}, "messages": {}, "metadata": {}, "prompt": {}, "request": {},
	"system": {}, "text": {}, "tool": {}, "tools": {}, "url": {}, "uri": {}, "user": {},
	"username": {}, "video": {}, "audio": {},
}

// FreezeRequestInput captures only request facts referenced by an expression so
// an async task can settle against the same request state after a restart.
// Raw request bodies and unrelated headers are deliberately never persisted.
func FreezeRequestInput(exprStr string, input RequestInput) (RequestInput, error) {
	_, body := ParseExprVersion(exprStr)
	tree, err := parser.Parse(body)
	if err != nil {
		return RequestInput{}, fmt.Errorf("freeze billing request: %w", err)
	}

	frozen := RequestInput{Timestamp: input.Timestamp}
	if input.ImageCount != nil {
		count := *input.ImageCount
		frozen.ImageCount = &count
	}
	if frozen.Timestamp <= 0 {
		frozen.Timestamp = time.Now().Unix()
	}
	normalizedHeaders := normalizeHeaders(input.Headers)

	var freezeErr error
	ast.Find(tree.Node, func(node ast.Node) bool {
		call, ok := node.(*ast.CallNode)
		if !ok || len(call.Arguments) != 1 {
			return false
		}
		callee, ok := call.Callee.(*ast.IdentifierNode)
		if !ok || (callee.Value != "header" && callee.Value != "param") {
			return false
		}
		literal, ok := call.Arguments[0].(*ast.StringNode)
		if !ok {
			freezeErr = fmt.Errorf("freeze billing request: %s() requires a literal key for async task billing", callee.Value)
			return true
		}
		key := strings.TrimSpace(literal.Value)
		if key == "" {
			return false
		}

		if callee.Value == "header" {
			normalizedKey := strings.ToLower(key)
			if _, sensitive := sensitiveBillingHeaders[normalizedKey]; sensitive {
				freezeErr = fmt.Errorf("freeze billing request: header %q is sensitive and cannot be persisted for async task billing", key)
				return true
			}
			if value, exists := normalizedHeaders[normalizedKey]; exists {
				if frozen.Headers == nil {
					frozen.Headers = make(map[string]string)
				}
				frozen.Headers[normalizedKey] = value
			}
			return false
		}

		if err := validatePersistedBillingParamPath(key); err != nil {
			freezeErr = fmt.Errorf("freeze billing request: param %q cannot be persisted: %w", key, err)
			return true
		}
		if value, exists := input.Params[key]; exists {
			value, keep, err := sanitizePersistedBillingParamValue(value)
			if err != nil {
				freezeErr = fmt.Errorf("freeze billing request: param %q cannot be persisted: %w", key, err)
				return true
			}
			if keep {
				if frozen.Params == nil {
					frozen.Params = make(map[string]any)
				}
				frozen.Params[key] = value
			}
			return false
		}
		if len(input.Body) == 0 {
			return false
		}
		result := gjson.GetBytes(input.Body, key)
		if result.Exists() {
			value, keep, err := sanitizePersistedBillingParamValue(result.Value())
			if err != nil {
				freezeErr = fmt.Errorf("freeze billing request: param %q cannot be persisted: %w", key, err)
				return true
			}
			if keep {
				if frozen.Params == nil {
					frozen.Params = make(map[string]any)
				}
				frozen.Params[key] = value
			}
		}
		return false
	})
	if freezeErr != nil {
		return RequestInput{}, freezeErr
	}
	return frozen, nil
}

func validatePersistedBillingParamPath(path string) error {
	if path == "" || len(path) > maxPersistedBillingParamPathBytes {
		return fmt.Errorf("path must be 1..%d bytes", maxPersistedBillingParamPathBytes)
	}
	segments := strings.Split(path, ".")
	if len(segments) > maxPersistedBillingParamDepth {
		return fmt.Errorf("path depth exceeds %d", maxPersistedBillingParamDepth)
	}
	for i, segment := range segments {
		if segment == "" {
			return fmt.Errorf("empty path segment")
		}
		for _, r := range segment {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
				(r >= '0' && r <= '9') || r == '_' || r == '-' {
				continue
			}
			return fmt.Errorf("advanced JSON selectors are not allowed")
		}
		normalized := normalizeBillingParamSegment(segment)
		if _, blocked := sensitiveBillingParamSegments[normalized]; blocked {
			return fmt.Errorf("sensitive path segment %q", segment)
		}
		if i == len(segments)-1 {
			if _, blocked := contentBillingParamLeaves[normalized]; blocked {
				return fmt.Errorf("content-bearing leaf %q", segment)
			}
		}
	}
	return nil
}

func normalizeBillingParamSegment(segment string) string {
	segment = strings.ToLower(strings.TrimSpace(segment))
	segment = strings.ReplaceAll(segment, "_", "")
	segment = strings.ReplaceAll(segment, "-", "")
	return segment
}

func sanitizePersistedBillingParamValue(value any) (any, bool, error) {
	switch v := value.(type) {
	case nil:
		return nil, false, nil
	case bool:
		return v, true, nil
	case string:
		if len(v) > maxPersistedBillingParamStringBytes {
			return nil, false, fmt.Errorf("string exceeds %d bytes", maxPersistedBillingParamStringBytes)
		}
		trimmed := strings.ToLower(strings.TrimSpace(v))
		if strings.HasPrefix(trimmed, "bearer ") || strings.HasPrefix(trimmed, "basic ") ||
			strings.HasPrefix(trimmed, "sk-") || strings.Contains(trimmed, "-----begin ") {
			return nil, false, fmt.Errorf("value resembles a credential")
		}
		return v, true, nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, false, fmt.Errorf("non-finite number")
		}
		return v, true, nil
	case float32:
		f := float64(v)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, false, fmt.Errorf("non-finite number")
		}
		return v, true, nil
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return v, true, nil
	default:
		return nil, false, fmt.Errorf("only scalar boolean, number, or short string values are allowed")
	}
}

// UsesRequestContext reports whether settlement must reuse request-time facts.
func UsesRequestContext(exprStr string) bool {
	vars := UsedVars(exprStr)
	for _, name := range []string{"header", "param", "hour", "minute", "weekday", "month", "day"} {
		if vars[name] {
			return true
		}
	}
	return false
}
