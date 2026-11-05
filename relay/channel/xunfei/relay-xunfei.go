package xunfei

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/samber/lo"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// https://console.xfyun.cn/services/cbm
// https://www.xfyun.cn/doc/spark/Web.html

func requestOpenAI2Xunfei(request dto.GeneralOpenAIRequest, xunfeiAppId string, domain string) *XunfeiChatRequest {
	messages := make([]XunfeiMessage, 0, len(request.Messages))
	shouldCovertSystemMessage := !strings.HasSuffix(request.Model, "3.5")
	for _, message := range request.Messages {
		if message.Role == "system" && shouldCovertSystemMessage {
			messages = append(messages, XunfeiMessage{
				Role:    "user",
				Content: message.StringContent(),
			})
			messages = append(messages, XunfeiMessage{
				Role:    "assistant",
				Content: "Okay",
			})
		} else {
			messages = append(messages, XunfeiMessage{
				Role:    message.Role,
				Content: message.StringContent(),
			})
		}
	}
	xunfeiRequest := XunfeiChatRequest{}
	xunfeiRequest.Header.AppId = xunfeiAppId
	xunfeiRequest.Parameter.Chat.Domain = domain
	xunfeiRequest.Parameter.Chat.Temperature = request.Temperature
	xunfeiRequest.Parameter.Chat.TopK = lo.FromPtrOr(request.N, 0)
	xunfeiRequest.Parameter.Chat.MaxTokens = request.GetMaxTokens()
	xunfeiRequest.Payload.Message.Text = messages
	return &xunfeiRequest
}

func responseXunfei2OpenAI(response *XunfeiChatResponse) *dto.OpenAITextResponse {
	if len(response.Payload.Choices.Text) == 0 {
		response.Payload.Choices.Text = []XunfeiChatResponseTextItem{
			{
				Content: "",
			},
		}
	}
	choice := dto.OpenAITextResponseChoice{
		Index: 0,
		Message: dto.Message{
			Role:    "assistant",
			Content: response.Payload.Choices.Text[0].Content,
		},
		FinishReason: constant.FinishReasonStop,
	}
	fullTextResponse := dto.OpenAITextResponse{
		Object:  "chat.completion",
		Created: common.GetTimestamp(),
		Choices: []dto.OpenAITextResponseChoice{choice},
		Usage:   response.Payload.Usage.Text,
	}
	return &fullTextResponse
}

func streamResponseXunfei2OpenAI(xunfeiResponse *XunfeiChatResponse) *dto.ChatCompletionsStreamResponse {
	if len(xunfeiResponse.Payload.Choices.Text) == 0 {
		xunfeiResponse.Payload.Choices.Text = []XunfeiChatResponseTextItem{
			{
				Content: "",
			},
		}
	}
	var choice dto.ChatCompletionsStreamResponseChoice
	choice.Delta.SetContentString(xunfeiResponse.Payload.Choices.Text[0].Content)
	if xunfeiResponse.Payload.Choices.Status == 2 {
		choice.FinishReason = &constant.FinishReasonStop
	}
	response := dto.ChatCompletionsStreamResponse{
		Object:  "chat.completion.chunk",
		Created: common.GetTimestamp(),
		Model:   "SparkDesk",
		Choices: []dto.ChatCompletionsStreamResponseChoice{choice},
	}
	return &response
}

func buildXunfeiAuthUrl(hostUrl string, apiKey, apiSecret string) string {
	HmacWithShaToBase64 := func(algorithm, data, key string) string {
		mac := hmac.New(sha256.New, []byte(key))
		mac.Write([]byte(data))
		encodeData := mac.Sum(nil)
		return base64.StdEncoding.EncodeToString(encodeData)
	}
	ul, err := url.Parse(hostUrl)
	if err != nil {
		fmt.Println(err)
	}
	date := time.Now().UTC().Format(time.RFC1123)
	signString := []string{"host: " + ul.Host, "date: " + date, "GET " + ul.Path + " HTTP/1.1"}
	sign := strings.Join(signString, "\n")
	sha := HmacWithShaToBase64("hmac-sha256", sign, apiSecret)
	authUrl := fmt.Sprintf("hmac username=\"%s\", algorithm=\"%s\", headers=\"%s\", signature=\"%s\"", apiKey,
		"hmac-sha256", "host date request-line", sha)
	authorization := base64.StdEncoding.EncodeToString([]byte(authUrl))
	v := url.Values{}
	v.Add("host", ul.Host)
	v.Add("date", date)
	v.Add("authorization", authorization)
	callUrl := hostUrl + "?" + v.Encode()
	return callUrl
}

func xunfeiStreamHandler(c *gin.Context, textRequest dto.GeneralOpenAIRequest, appId string, apiSecret string, apiKey string) (*dto.Usage, *types.NewAPIError) {
	domain, authUrl := getXunfeiAuthUrl(c, apiKey, apiSecret, textRequest.Model)
	dataChan, doneChan, cancel, err := xunfeiMakeRequest(c, textRequest, domain, authUrl, appId)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeDoRequestFailed)
	}
	defer cancel()
	helper.SetEventStreamHeaders(c)
	var usage dto.Usage
	var readErr error
	disconnected := c.Stream(func(w io.Writer) bool {
		select {
		case <-c.Request.Context().Done():
			readErr = c.Request.Context().Err()
			return false
		case xunfeiResponse := <-dataChan:
			usage.PromptTokens += xunfeiResponse.Payload.Usage.Text.PromptTokens
			usage.CompletionTokens += xunfeiResponse.Payload.Usage.Text.CompletionTokens
			usage.TotalTokens += xunfeiResponse.Payload.Usage.Text.TotalTokens
			response := streamResponseXunfei2OpenAI(&xunfeiResponse)
			jsonResponse, err := common.Marshal(response)
			if err != nil {
				readErr = err
				return false
			}
			c.Render(-1, common.CustomEvent{Data: "data: " + string(jsonResponse)})
			return true
		case readErr = <-doneChan:
			if readErr == nil {
				c.Render(-1, common.CustomEvent{Data: "data: [DONE]"})
			}
			return false
		}
	})
	if disconnected {
		return &usage, service.NewRelayClientGoneError(context.Canceled)
	}
	if readErr != nil {
		return &usage, types.NewError(readErr, types.ErrorCodeBadResponseBody)
	}
	return &usage, nil
}

func xunfeiHandler(c *gin.Context, textRequest dto.GeneralOpenAIRequest, appId string, apiSecret string, apiKey string) (*dto.Usage, *types.NewAPIError) {
	domain, authUrl := getXunfeiAuthUrl(c, apiKey, apiSecret, textRequest.Model)
	dataChan, doneChan, cancel, err := xunfeiMakeRequest(c, textRequest, domain, authUrl, appId)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeDoRequestFailed)
	}
	defer cancel()
	var usage dto.Usage
	var content strings.Builder
	var xunfeiResponse XunfeiChatResponse
readLoop:
	for {
		select {
		case <-c.Request.Context().Done():
			return &usage, types.NewError(c.Request.Context().Err(), types.ErrorCodeBadResponseBody)
		case xunfeiResponse = <-dataChan:
			if len(xunfeiResponse.Payload.Choices.Text) > 0 {
				content.WriteString(xunfeiResponse.Payload.Choices.Text[0].Content)
			}
			usage.PromptTokens += xunfeiResponse.Payload.Usage.Text.PromptTokens
			usage.CompletionTokens += xunfeiResponse.Payload.Usage.Text.CompletionTokens
			usage.TotalTokens += xunfeiResponse.Payload.Usage.Text.TotalTokens
		case err = <-doneChan:
			if err != nil {
				return &usage, types.NewError(err, types.ErrorCodeBadResponseBody)
			}
			break readLoop
		}
	}
	if len(xunfeiResponse.Payload.Choices.Text) == 0 {
		xunfeiResponse.Payload.Choices.Text = []XunfeiChatResponseTextItem{{}}
	}
	xunfeiResponse.Payload.Choices.Text[0].Content = content.String()
	xunfeiResponse.Payload.Usage.Text = usage

	response := responseXunfei2OpenAI(&xunfeiResponse)
	jsonResponse, err := common.Marshal(response)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	_, _ = c.Writer.Write(jsonResponse)
	return &usage, nil
}

// Xunfei sends its billable request from DoResponse, rather than the shared HTTP
// transport. Keep its dispatch gate, cancellation and response memory bound at
// the real WebSocket boundary. Closing a socket interrupts both ReadMessage and
// WriteMessage; cancellation also releases a reader whose consumer has stopped.
func xunfeiMakeRequest(c *gin.Context, textRequest dto.GeneralOpenAIRequest, domain, authUrl, appId string) (<-chan XunfeiChatResponse, <-chan error, context.CancelFunc, error) {
	common.SetContextKey(c, constant.ContextKeyUpstreamRequestAttempted, false)
	common.SetContextKey(c, constant.ContextKeyUpstreamRequestUnsent, true)
	common.SetContextKey(c, constant.ContextKeyUpstreamResponseDecodeFailed, false)
	common.SetContextKey(c, constant.ContextKeyUpstreamExplicitRejection, false)
	common.SetContextKey(c, constant.ContextKeyUpstreamResponseUsageIncomplete, false)
	if stopped := service.RelayRequestCancellation(c); stopped != nil {
		return nil, nil, nil, stopped
	}
	if apiErr := service.CheckModelTestAccess(c); apiErr != nil {
		return nil, nil, nil, apiErr
	}
	body, err := common.Marshal(requestOpenAI2Xunfei(textRequest, appId, domain))
	if err != nil {
		return nil, nil, nil, err
	}
	ctx, cancel := context.WithCancel(c.Request.Context())
	var stopClose func() bool
	d := websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
		NetDialContext: func(dialCtx context.Context, network, address string) (net.Conn, error) {
			conn, err := (&net.Dialer{}).DialContext(dialCtx, network, address)
			if err == nil {
				// DialContext alone does not interrupt a WebSocket handshake read
				// after TCP connects. Watch the parent for the entire socket life.
				stopClose = context.AfterFunc(ctx, func() { _ = conn.Close() })
			}
			return conn, err
		},
	}
	conn, resp, err := d.DialContext(ctx, authUrl, nil)
	if err != nil {
		cancel()
		if stopClose != nil {
			stopClose()
		}
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, nil, nil, err
	}
	cleanup := func() {
		cancel()
		stopClose()
		_ = conn.Close()
	}
	// Recheck after the handshake, immediately before the paid generation write.
	if apiErr := service.CheckModelTestAccess(c); apiErr != nil {
		cleanup()
		return nil, nil, nil, apiErr
	}
	if err := ctx.Err(); err != nil {
		cleanup()
		return nil, nil, nil, err
	}
	if origin, err := url.Parse(authUrl); err == nil {
		common.SetContextKey(c, constant.ContextKeyUpstreamRequestOrigin, origin.Scheme+"://"+origin.Host)
	}
	common.SetContextKey(c, constant.ContextKeyUpstreamRequestUnsent, false)
	common.SetContextKey(c, constant.ContextKeyUpstreamRequestAttempted, true)
	if err := conn.WriteMessage(websocket.TextMessage, body); err != nil {
		cleanup()
		return nil, nil, nil, err
	}

	const modelTestResponseLimit = 512 << 10
	modelTest := common.GetContextKeyBool(c, constant.ContextKeyModelTestSingleAttempt)
	if modelTest {
		conn.SetReadLimit(modelTestResponseLimit)
	}
	dataChan := make(chan XunfeiChatResponse)
	// A buffered result never strands the reader when a stream client disconnects.
	doneChan := make(chan error, 1)
	go func() {
		defer cleanup()
		defer close(doneChan)
		var received int64
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				if ctx.Err() != nil {
					err = ctx.Err()
				}
				doneChan <- err
				return
			}
			if modelTest {
				received += int64(len(msg))
				if received > modelTestResponseLimit {
					doneChan <- errors.New("controlled model test response exceeds limit")
					return
				}
			}
			var response XunfeiChatResponse
			if err := common.Unmarshal(msg, &response); err != nil {
				doneChan <- err
				return
			}
			if response.Header.Code != 0 {
				doneChan <- fmt.Errorf("xunfei returned error code %d", response.Header.Code)
				return
			}
			select {
			case dataChan <- response:
			case <-ctx.Done():
				doneChan <- ctx.Err()
				return
			}
			if response.Payload.Choices.Status == 2 {
				doneChan <- nil
				return
			}
		}
	}()
	return dataChan, doneChan, cleanup, nil
}

func apiVersion2domain(apiVersion string) string {
	switch apiVersion {
	case "v1.1":
		return "lite"
	case "v2.1":
		return "generalv2"
	case "v3.1":
		return "generalv3"
	case "v3.5":
		return "generalv3.5"
	case "v4.0":
		return "4.0Ultra"
	}
	return "general" + apiVersion
}

func getXunfeiAuthUrl(c *gin.Context, apiKey string, apiSecret string, modelName string) (string, string) {
	apiVersion := getAPIVersion(c, modelName)
	domain := apiVersion2domain(apiVersion)
	authUrl := buildXunfeiAuthUrl(fmt.Sprintf("wss://spark-api.xf-yun.com/%s/chat", apiVersion), apiKey, apiSecret)
	return domain, authUrl
}

func getAPIVersion(c *gin.Context, modelName string) string {
	query := c.Request.URL.Query()
	apiVersion := query.Get("api-version")
	if apiVersion != "" {
		return apiVersion
	}
	parts := strings.Split(modelName, "-")
	if len(parts) == 2 {
		apiVersion = parts[1]
		return apiVersion

	}
	apiVersion = c.GetString("api_version")
	if apiVersion != "" {
		return apiVersion
	}
	apiVersion = "v1.1"
	common.SysLog(common.LogText("api_version not found, using default: %s", apiVersion))
	return apiVersion
}
