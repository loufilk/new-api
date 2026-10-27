package openai

import (
	"fmt"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/pkg/privacy"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func OpenaiRealtimeHandler(c *gin.Context, info *relaycommon.RelayInfo) (*types.NewAPIError, *dto.RealtimeUsage) {
	if info == nil || info.ClientWs == nil || info.TargetWs == nil {
		return types.NewError(fmt.Errorf("invalid websocket connection"), types.ErrorCodeBadResponse), nil
	}
	info.IsStream = true
	clientConn, targetConn := info.ClientWs, info.TargetWs
	clientClosed, targetClosed := make(chan struct{}), make(chan struct{})
	stop := make(chan struct{})
	errChan := make(chan error, 2)
	reportError := func(err error) {
		select {
		case errChan <- err:
		default:
		}
	}
	requestDone := c.Done()
	if c.Request != nil {
		requestDone = c.Request.Context().Done()
	}

	// Both readers inspect mutable formats/tools and contribute local estimates.
	// A single lock owns all of those fields and usage. Final settlement only
	// sees a frozen snapshot after both connections are stopped and readers join.
	var usageMu sync.Mutex
	var readers sync.WaitGroup
	localUsage, sumUsage := &dto.RealtimeUsage{}, &dto.RealtimeUsage{}
	readers.Add(2)
	gopool.Go(func() {
		defer readers.Done()
		defer close(clientClosed)
		defer func() {
			if r := recover(); r != nil {
				reportError(fmt.Errorf("panic in client reader: %v", r))
			}
		}()
		for {
			select {
			case <-requestDone:
				return
			case <-stop:
				return
			default:
			}
			_, message, err := clientConn.ReadMessage()
			if err != nil {
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					reportError(fmt.Errorf("error reading from client: %v", err))
				}
				return
			}
			err = func() error {
				usageMu.Lock()
				defer usageMu.Unlock()
				event := &dto.RealtimeEvent{}
				if err := common.Unmarshal(message, event); err != nil {
					return err
				}
				if event.Type == dto.RealtimeEventTypeSessionUpdate && event.Session != nil && event.Session.Tools != nil {
					info.RealtimeTools = event.Session.Tools
				}
				text, audio, err := service.CountTokenRealtime(info, *event, info.UpstreamModelName)
				if err != nil {
					return err
				}
				if err := dto.AddRealtimeUsage(localUsage, &dto.RealtimeUsage{TotalTokens: text + audio, InputTokens: text + audio, InputTokenDetails: dto.InputTokenDetails{TextTokens: text, AudioTokens: audio}}); err != nil {
					service.PreserveUnknownBillingDelivery(info)
					return err
				}
				return nil
			}()
			if err != nil {
				reportError(err)
				return
			}
			if err = helper.WssString(c, targetConn, string(message)); err != nil {
				reportError(err)
				return
			}
		}
	})
	gopool.Go(func() {
		defer readers.Done()
		defer close(targetClosed)
		defer func() {
			if r := recover(); r != nil {
				reportError(fmt.Errorf("panic in target reader: %v", r))
			}
		}()
		for {
			select {
			case <-requestDone:
				return
			case <-stop:
				return
			default:
			}
			_, message, err := targetConn.ReadMessage()
			if err != nil {
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					reportError(fmt.Errorf("error reading from target: %v", err))
				}
				return
			}
			err = func() error {
				usageMu.Lock()
				defer usageMu.Unlock()
				info.SetFirstResponseTime()
				event := &dto.RealtimeEvent{}
				if err := common.Unmarshal(message, event); err != nil {
					return err
				}
				if event.Type == dto.RealtimeEventTypeResponseDone {
					if event.Response != nil && event.Response.Usage != nil {
						segment := event.Response.Usage.Clone()
						// Move ownership before reserving: a failed reservation must not
						// make the final flush observe this response a second time.
						localUsage = &dto.RealtimeUsage{}
						return preConsumeUsage(c, info, &segment, sumUsage)
					}
					text, audio, err := service.CountTokenRealtime(info, *event, info.UpstreamModelName)
					if err != nil {
						return err
					}
					info.IsFirstRequest = false
					if err := dto.AddRealtimeUsage(localUsage, &dto.RealtimeUsage{TotalTokens: text + audio, InputTokens: text + audio, InputTokenDetails: dto.InputTokenDetails{TextTokens: text, AudioTokens: audio}}); err != nil {
						service.PreserveUnknownBillingDelivery(info)
						return err
					}
					segment := localUsage.Clone()
					localUsage = &dto.RealtimeUsage{}
					return preConsumeUsage(c, info, &segment, sumUsage)
				}
				if event.Type == dto.RealtimeEventTypeSessionUpdated || event.Type == dto.RealtimeEventTypeSessionCreated {
					if event.Session != nil {
						info.InputAudioFormat = common.GetStringIfEmpty(event.Session.InputAudioFormat, info.InputAudioFormat)
						info.OutputAudioFormat = common.GetStringIfEmpty(event.Session.OutputAudioFormat, info.OutputAudioFormat)
					}
					return nil
				}
				text, audio, err := service.CountTokenRealtime(info, *event, info.UpstreamModelName)
				if err != nil {
					return err
				}
				if err := dto.AddRealtimeUsage(localUsage, &dto.RealtimeUsage{TotalTokens: text + audio, OutputTokens: text + audio, OutputTokenDetails: dto.OutputTokenDetails{TextTokens: text, AudioTokens: audio}}); err != nil {
					service.PreserveUnknownBillingDelivery(info)
					return err
				}
				return nil
			}()
			if err != nil {
				reportError(err)
				return
			}
			if err := helper.WssString(c, clientConn, string(privacy.SanitizeClientPayload(c, message))); err != nil {
				reportError(err)
				return
			}
		}
	})
	select {
	case <-clientClosed:
	case <-targetClosed:
	case err := <-errChan:
		logger.LogError(c, common.LogText("realtime error: %s", err.Error()))
	case <-requestDone:
	}
	close(stop)
	_ = clientConn.Close()
	_ = targetConn.Close()
	readers.Wait()
	if localUsage.TotalTokens != 0 {
		_ = preConsumeUsage(c, info, localUsage, sumUsage)
	}
	return nil, sumUsage
}

func preConsumeUsage(ctx *gin.Context, info *relaycommon.RelayInfo, usage, totalUsage *dto.RealtimeUsage) error {
	if err := dto.AddRealtimeUsage(totalUsage, usage); err != nil {
		service.PreserveUnknownBillingDelivery(info)
		return err
	}
	return service.PreWssConsumeQuota(ctx, info, usage)
}
