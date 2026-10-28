package middleware

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting"

	"github.com/gin-gonic/gin"
)

const (
	ModelRequestRateLimitCountMark        = "MRRL"
	ModelRequestRateLimitSuccessCountMark = "MRRLS"
	modelRateLimitTimeFormat              = "2006-01-02T15:04:05.000Z"
)

// Count total attempts first, reserve one success slot, then reconcile the
// response outcome. This is the same admission order as the memory backend.
func redisRateLimitHandler(subject string, duration int64, totalMaxCount, successMaxCount int) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithCancel(c.Request.Context())
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		allowed, err := allowRedisModelAttempt(ctx, common.RDB, "rateLimit:"+ModelRequestRateLimitCountMark+":"+subject+":v2", totalMaxCount, duration)
		if err != nil {
			abortWithOpenAiMessage(c, http.StatusInternalServerError, "rate_limit_check_failed")
			return
		}
		if !allowed {
			abortWithOpenAiMessage(c, http.StatusTooManyRequests, "model_total_request_limit_exceeded")
			return
		}
		reservation, allowed, err := reserveRedisModelSuccess(ctx, common.RDB, "rateLimit:"+ModelRequestRateLimitSuccessCountMark+":"+subject, successMaxCount, duration, cancel)
		if err != nil {
			abortWithOpenAiMessage(c, http.StatusInternalServerError, "rate_limit_check_failed")
			return
		}
		if !allowed {
			abortWithOpenAiMessage(c, http.StatusTooManyRequests, "model_success_request_limit_exceeded")
			return
		}
		defer reservation.Complete(false)
		c.Next()
		reservation.Complete(modelRequestSucceeded(c))
	}
}

// 内存限流处理器
func memoryRateLimitHandler(subject string, duration int64, totalMaxCount, successMaxCount int) gin.HandlerFunc {
	inMemoryRateLimiter.Init(time.Duration(setting.ModelRequestRateLimitDurationMinutes) * time.Minute)

	return func(c *gin.Context) {
		totalKey := ModelRequestRateLimitCountMark + subject
		successKey := ModelRequestRateLimitSuccessCountMark + subject

		// 1. 检查总请求数限制（当totalMaxCount为0时跳过）
		if totalMaxCount > 0 && !inMemoryRateLimiter.Request(totalKey, totalMaxCount, duration) {
			abortWithOpenAiMessage(c, http.StatusTooManyRequests, "model_total_request_limit_exceeded")
			return
		}

		var reservation *common.RateLimitReservation
		if successMaxCount > 0 {
			reservation = inMemoryRateLimiter.Reserve(successKey, successMaxCount, duration)
			if reservation == nil {
				abortWithOpenAiMessage(c, http.StatusTooManyRequests, "model_success_request_limit_exceeded")
				return
			}
			defer reservation.Complete(false)
		}

		// 3. 处理请求
		c.Next()

		// 4. 如果请求成功，记录到实际的成功请求计数中
		reservation.Complete(modelRequestSucceeded(c))
	}
}

func modelRequestSucceeded(c *gin.Context) bool {
	status, _ := common.GetContextKeyType[*relaycommon.StreamStatus](c, constant.ContextKeyResponseStreamStatus)
	return c.Writer.Status() < 400 && !status.ResponseFailed()
}

// ModelRequestRateLimit 模型请求限流中间件
func ModelRequestRateLimit() func(c *gin.Context) {
	return func(c *gin.Context) {
		// 在每个请求时检查是否启用限流
		if !setting.ModelRequestRateLimitEnabled {
			c.Next()
			return
		}

		// 计算限流参数
		duration := rateLimitDurationSeconds(setting.ModelRequestRateLimitDurationMinutes)
		totalMaxCount := setting.ModelRequestRateLimitCount
		successMaxCount := setting.ModelRequestRateLimitSuccessCount

		// 获取分组
		group := common.GetContextKeyString(c, constant.ContextKeyTokenGroup)
		if group == "" {
			group = common.GetContextKeyString(c, constant.ContextKeyUserGroup)
		}

		// 获取分组的限流配置。配置了独立限流的分组按用户和分组单独计数，
		// 其余分组共用该用户的全局计数
		subject := strconv.Itoa(c.GetInt("id"))
		groupTotalCount, groupSuccessCount, found := setting.GetGroupRateLimit(group)
		if found {
			totalMaxCount = groupTotalCount
			successMaxCount = groupSuccessCount
			subject += ":" + group
		}

		if duration <= 0 || duration > int64((3650*24*time.Hour)/time.Second) || totalMaxCount < 0 || successMaxCount < 0 {
			abortWithOpenAiMessage(c, http.StatusInternalServerError, "invalid_rate_limit_configuration")
			return
		}

		// 根据存储类型选择并执行限流处理器
		if common.RedisEnabled {
			redisRateLimitHandler(subject, duration, totalMaxCount, successMaxCount)(c)
		} else {
			memoryRateLimitHandler(subject, duration, totalMaxCount, successMaxCount)(c)
		}
	}
}

func rateLimitDurationSeconds(durationMinutes int) int64 {
	if durationMinutes <= 0 {
		return 0
	}
	minutes := int64(durationMinutes)
	if minutes > math.MaxInt64/60 {
		return math.MaxInt64
	}
	return minutes * 60
}

func rateLimitCapacity(count int, durationSeconds int64) int64 {
	if count <= 0 || durationSeconds <= 0 {
		return 0
	}
	c := int64(count)
	if c > math.MaxInt64/durationSeconds {
		return math.MaxInt64
	}
	return c * durationSeconds
}
