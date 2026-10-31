package service

import (
	"fmt"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// UserCallReply describes an authenticated request, not a client-selected policy.
// Only the gateway's route classifier may select a supported reply protocol.
const (
	CallIdentityAPIKey             = "api_key"
	CallIdentityDashboard          = "dashboard_session"
	CallIdentityArtifactCapability = "artifact_capability"
	CallIdentityPublicArtifact     = "public_artifact"
)

type UserCallReply struct {
	IdentityKind string // server-derived; never bind to client input
	Protocol     string
	Model        string
	Stream       bool
	JSONArray    bool // Gemini streamGenerateContent without alt=sse.
}

func (reply UserCallReply) SupportsText() bool {
	switch reply.Protocol {
	case "chat", "playground", "completions", "responses", "claude", "gemini":
		return true
	default:
		return false
	}
}

// WriteUserCallInterception persists the decision before writing any response.
// A database failure never releases a request to a channel or upstream. The hit
// status is the selected HTTP status, not evidence that the peer received it.
func WriteUserCallInterception(c *gin.Context, policy *model.UserCallInterception, userID, tokenID int, reply UserCallReply) {
	status := http.StatusForbidden
	if reply.SupportsText() {
		status = http.StatusOK
	}
	requestID := c.GetString(common.RequestIdKey)
	if requestID == "" {
		requestID = common.NewRequestId()
		c.Set(common.RequestIdKey, requestID)
		c.Header(common.RequestIdKey, requestID)
	}
	// FullPath is a server-owned template; never store a raw URL, query, model,
	// task identifier, body, custom reply, or credential in this log.
	route := c.FullPath()
	if route == "" {
		route = "unregistered"
	}
	identityKind := reply.IdentityKind
	actorID := c.GetInt("id")
	if identityKind == "" {
		identityKind = CallIdentityDashboard
		if tokenID > 0 {
			identityKind = CallIdentityAPIKey
		}
	}
	if identityKind == CallIdentityArtifactCapability || identityKind == CallIdentityPublicArtifact {
		actorID, tokenID = 0, 0
	}
	err := model.RecordUserCallInterceptionHit(&model.UserCallInterceptionHit{
		UserID: userID, ActorUserID: actorID, IdentityKind: identityKind, TokenID: tokenID, RequestID: requestID,
		Interface: c.Request.Method + " " + route, Protocol: reply.Protocol,
		Status: status, Revision: policy.Revision,
	})
	if err != nil {
		// Do not print the DB error: driver errors can contain parameter values.
		common.SysError("user call interception hit could not be persisted")
		WriteUserCallInterceptionUnavailable(c)
		return
	}
	c.Abort()
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Newapi-Call-Intercepted", "true")
	if !reply.SupportsText() {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{
			"message": i18n.T(c, "relay.user_call_intercepted"),
			"type":    "user_call_intercepted", "code": "user_call_intercepted",
		}})
		return
	}
	WriteUserCallReply(c, reply, policy.Reply, requestID)
}

func WriteUserCallInterceptionUnavailable(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
		"message": i18n.T(c, "relay.user_call_interception_unavailable"),
		"type":    "user_call_interception_unavailable", "code": "user_call_interception_unavailable",
	}})
}

// InterceptUserArtifact is shared by capability-backed and legacy media routes.
// Its user ID comes from the authenticated/validated artifact's database owner,
// never a caller-supplied user ID. It reveals no custom reply to anonymous peers.
func InterceptUserArtifact(c *gin.Context, userID int, identityKind string) bool {
	policy, err := model.GetUserCallInterception(userID)
	if err != nil {
		WriteUserCallInterceptionUnavailable(c)
		return true
	}
	if !policy.Enabled {
		return false
	}
	WriteUserCallInterception(c, policy, userID, c.GetInt("token_id"), UserCallReply{Protocol: "artifact", IdentityKind: identityKind})
	return true
}

// WriteUserCallReply emits only local text and explicitly zero usage. It cannot
// select channels, invoke upstream adapters, create tasks or touch billing.
func WriteUserCallReply(c *gin.Context, request UserCallReply, text, requestID string) {
	created := time.Now().Unix()
	id := "chatcmpl_" + requestID
	usage := gin.H{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}
	if request.Stream && !request.JSONArray {
		c.Header("Content-Type", "text/event-stream; charset=utf-8")
		c.Header("Cache-Control", "private, no-store")
		c.Header("X-Accel-Buffering", "no")
		c.Status(http.StatusOK)
	}
	switch request.Protocol {
	case "chat", "playground":
		if !request.Stream {
			c.JSON(http.StatusOK, gin.H{"id": id, "object": "chat.completion", "created": created, "model": request.Model,
				"choices": []any{gin.H{"index": 0, "message": gin.H{"role": "assistant", "content": text}, "finish_reason": "stop"}}, "usage": usage})
			return
		}
		for _, delta := range []gin.H{{"role": "assistant", "content": ""}, {"content": text}} {
			if !writeUserCallEvent(c, "", gin.H{"id": id, "object": "chat.completion.chunk", "created": created, "model": request.Model,
				"choices": []any{gin.H{"index": 0, "delta": delta, "finish_reason": nil}}}) {
				return
			}
		}
		if !writeUserCallEvent(c, "", gin.H{"id": id, "object": "chat.completion.chunk", "created": created, "model": request.Model,
			"choices": []any{gin.H{"index": 0, "delta": gin.H{}, "finish_reason": "stop"}}, "usage": usage}) {
			return
		}
		_, _ = fmt.Fprint(c.Writer, "data: [DONE]\n\n")
		c.Writer.Flush()
	case "completions":
		response := gin.H{"id": id, "object": "text_completion", "created": created, "model": request.Model,
			"choices": []any{gin.H{"index": 0, "text": text, "logprobs": nil, "finish_reason": "stop"}}, "usage": usage}
		if !request.Stream {
			c.JSON(http.StatusOK, response)
			return
		}
		if !writeUserCallEvent(c, "", response) {
			return
		}
		_, _ = fmt.Fprint(c.Writer, "data: [DONE]\n\n")
		c.Writer.Flush()
	case "responses":
		writeUserCallResponses(c, request, text, requestID, created)
	case "claude":
		message := gin.H{"id": "msg_" + requestID, "type": "message", "role": "assistant", "model": request.Model,
			"content": []any{gin.H{"type": "text", "text": text}}, "stop_reason": "end_turn", "stop_sequence": nil,
			"usage": gin.H{"input_tokens": 0, "output_tokens": 0}}
		if !request.Stream {
			c.JSON(http.StatusOK, message)
			return
		}
		message["content"], message["stop_reason"] = []any{}, nil
		events := []gin.H{
			{"type": "message_start", "message": message},
			{"type": "content_block_start", "index": 0, "content_block": gin.H{"type": "text", "text": ""}},
			{"type": "content_block_delta", "index": 0, "delta": gin.H{"type": "text_delta", "text": text}},
			{"type": "content_block_stop", "index": 0},
			{"type": "message_delta", "delta": gin.H{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": gin.H{"output_tokens": 0}},
			{"type": "message_stop"},
		}
		for _, event := range events {
			if !writeUserCallEvent(c, event["type"].(string), event) {
				return
			}
		}
	case "gemini":
		response := gin.H{"candidates": []any{gin.H{"content": gin.H{"role": "model", "parts": []any{gin.H{"text": text}}}, "finishReason": "STOP", "index": 0}},
			"usageMetadata": gin.H{"promptTokenCount": 0, "candidatesTokenCount": 0, "totalTokenCount": 0}, "modelVersion": request.Model, "responseId": requestID}
		if request.JSONArray {
			c.JSON(http.StatusOK, []any{response})
			return
		}
		if !request.Stream {
			c.JSON(http.StatusOK, response)
			return
		}
		writeUserCallEvent(c, "", response)
	}
}

func writeUserCallResponses(c *gin.Context, request UserCallReply, text, requestID string, created int64) {
	part := gin.H{"type": "output_text", "text": text, "annotations": []any{}, "logprobs": []any{}}
	item := gin.H{"id": "msg_" + requestID, "type": "message", "role": "assistant", "status": "completed", "content": []any{part}}
	response := gin.H{"id": "resp_" + requestID, "object": "response", "created_at": created, "status": "completed", "model": request.Model,
		"output": []any{item}, "error": nil, "incomplete_details": nil, "metadata": gin.H{},
		"usage": gin.H{"input_tokens": 0, "output_tokens": 0, "total_tokens": 0, "input_tokens_details": gin.H{"cached_tokens": 0}, "output_tokens_details": gin.H{"reasoning_tokens": 0}}}
	if !request.Stream {
		c.JSON(http.StatusOK, response)
		return
	}
	started := gin.H{"id": response["id"], "object": "response", "created_at": created, "status": "in_progress", "model": request.Model, "output": []any{}, "error": nil, "incomplete_details": nil, "usage": nil}
	initialItem := gin.H{"id": item["id"], "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}
	initialPart := gin.H{"type": "output_text", "text": "", "annotations": []any{}, "logprobs": []any{}}
	events := []gin.H{
		{"type": "response.created", "response": started},
		{"type": "response.in_progress", "response": started},
		{"type": "response.output_item.added", "output_index": 0, "item": initialItem},
		{"type": "response.content_part.added", "output_index": 0, "item_id": item["id"], "content_index": 0, "part": initialPart},
		{"type": "response.output_text.delta", "output_index": 0, "item_id": item["id"], "content_index": 0, "delta": text, "logprobs": []any{}},
		{"type": "response.output_text.done", "output_index": 0, "item_id": item["id"], "content_index": 0, "text": text, "logprobs": []any{}},
		{"type": "response.content_part.done", "output_index": 0, "item_id": item["id"], "content_index": 0, "part": part},
		{"type": "response.output_item.done", "output_index": 0, "item": item},
		{"type": "response.completed", "response": response},
	}
	for sequence, event := range events {
		event["sequence_number"] = sequence
		if !writeUserCallEvent(c, event["type"].(string), event) {
			return
		}
	}
}

func writeUserCallEvent(c *gin.Context, event string, data gin.H) bool {
	encoded, err := common.Marshal(data)
	if err != nil || c.Request.Context().Err() != nil {
		return false
	}
	if event != "" {
		if _, err = fmt.Fprintf(c.Writer, "event: %s\n", event); err != nil {
			return false
		}
	}
	if _, err = fmt.Fprintf(c.Writer, "data: %s\n\n", encoded); err != nil {
		return false
	}
	c.Writer.Flush()
	return true
}
