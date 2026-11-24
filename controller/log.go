package controller

import (
	"math"
	"net/http"
	"net/url"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// logFilterFromQuery reads the search shared by lists, exports and statistics.
// User ID, username and channel are administrator filters. Self requests always
// use the authenticated ID, never a mutable username or a caller-supplied ID.
func logFilterFromQuery(c *gin.Context, admin bool) (model.LogFilter, error) {
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	filter := model.LogFilter{
		Type:              logType,
		StartTimestamp:    startTimestamp,
		EndTimestamp:      endTimestamp,
		ModelName:         c.Query("model_name"),
		TokenName:         c.Query("token_name"),
		Group:             c.Query("group"),
		RequestId:         c.Query("request_id"),
		UpstreamRequestId: c.Query("upstream_request_id"),
	}
	if admin {
		filter.Username = c.Query("username")
		filter.Channel, _ = strconv.Atoi(c.Query("channel"))
		// Gin's query helpers discard malformed query pairs. Reject the raw
		// parse error instead of silently turning an invalid ID into no filter.
		query, err := url.ParseQuery(c.Request.URL.RawQuery)
		if err != nil {
			return filter, common.NewMessage("User ID must be a positive integer no greater than 9223372036854775807")
		}
		values := query["user_id"]
		if len(values) > 1 {
			return filter, common.NewMessage("User ID must be a positive integer no greater than 9223372036854775807")
		}
		if len(values) == 1 && values[0] != "" {
			value := values[0]
			for _, digit := range value {
				if digit < '0' || digit > '9' {
					return filter, common.NewMessage("User ID must be a positive integer no greater than 9223372036854775807")
				}
			}
			userID, err := strconv.Atoi(value)
			if err != nil || userID <= 0 {
				return filter, common.NewMessage("User ID must be a positive integer no greater than 9223372036854775807")
			}
			filter.UserId = userID
		}
	} else {
		filter.UserId = c.GetInt("id")
		if filter.UserId <= 0 {
			return filter, common.NewMessage("Unauthorized, invalid user info")
		}
	}
	return filter, nil
}

// limitLogTimeRange returns the start of a log or dashboard search kept within
// the longest log time range. A missing start reaches back the whole range, and
// time after now does not count toward it.
func limitLogTimeRange(startTimestamp int64, endTimestamp int64) (int64, error) {
	return limitLogTimeRangeWithMax(startTimestamp, endTimestamp, operation_setting.GetLogSetting().MaxRangeSeconds())
}

func limitLogTimeRangeWithMax(startTimestamp, endTimestamp, maxRange int64) (int64, error) {
	if maxRange <= 0 {
		return 0, common.NewMessage("Invalid parameters")
	}
	end := common.GetTimestamp()
	if endTimestamp != 0 {
		end = min(end, endTimestamp)
	}
	// Timestamps supplied by a caller may be MinInt64. Subtraction must
	// not wrap and admit an unbounded range (or fabricate a future start).
	lower := int64(math.MinInt64)
	if end >= math.MinInt64+maxRange {
		lower = end - maxRange
	}
	if startTimestamp == 0 {
		return lower, nil
	}
	if startTimestamp < lower {
		return 0, common.NewMessage("The time range cannot exceed {{days}} days", map[string]any{"days": maxRange / (24 * 60 * 60)})
	}
	return startTimestamp, nil
}

// Invalid negative limits disable GORM pagination. Bound both the SQL limit
// and index arithmetic (including the synthetic IDs in a self response).
func logPageQuery(c *gin.Context) *common.PageInfo {
	page := common.GetPageQuery(c)
	if page.PageSize < 1 || page.Page < 1 || page.Page > math.MaxInt/page.PageSize {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return nil
	}
	return page
}

func GetAllLogs(c *gin.Context) {
	pageInfo := logPageQuery(c)
	if pageInfo == nil {
		return
	}
	filter, err := logFilterFromQuery(c, true)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, err)
		return
	}
	filter.SkipTotal = c.Query("skip_total") == "1"
	logs, total, err := model.GetAllLogs(filter, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if c.GetInt("role") < common.RoleRootUser {
		model.FormatAdminLogs(logs)
	} else {
		model.FormatRootLogs(logs)
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(logs)
	common.ApiSuccess(c, pageInfo)
	return
}

func GetUserLogs(c *gin.Context) {
	pageInfo := logPageQuery(c)
	if pageInfo == nil {
		return
	}
	filter, err := logFilterFromQuery(c, false)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusUnauthorized, err)
		return
	}
	if c.GetInt("role") < common.RoleAdminUser {
		startTimestamp, err := limitLogTimeRange(filter.StartTimestamp, filter.EndTimestamp)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		filter.StartTimestamp = startTimestamp
	}
	filter.SkipTotal = c.Query("skip_total") == "1"
	logs, total, err := model.GetUserLogs(c.GetInt("id"), filter, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(logs)
	common.ApiSuccess(c, pageInfo)
	return
}

// ExportAllLogs streams every user's logs matching the search.
func ExportAllLogs(c *gin.Context) {
	exportLogs(c, true)
}

// ExportUserLogs streams the caller's own logs matching the search.
func ExportUserLogs(c *gin.Context) {
	if c.GetInt("role") < common.RoleAdminUser && !operation_setting.GetLogSetting().UserExportEnabled {
		common.ApiErrorT(c, "Log export is disabled")
		return
	}
	exportLogs(c, false)
}

// exportLogs writes one JSON log per line, newest first, and ends with an API
// response object: success with the log count, or the failure. Each user runs
// one export at a time, and every export stays within the longest log time
// range.
func exportLogs(c *gin.Context, all bool) {
	filter, err := logFilterFromQuery(c, all)
	if err != nil {
		status := http.StatusBadRequest
		if !all {
			status = http.StatusUnauthorized
		}
		common.ApiErrorStatus(c, status, err)
		return
	}
	startTimestamp, err := limitLogTimeRange(filter.StartTimestamp, filter.EndTimestamp)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	filter.StartTimestamp = startTimestamp

	userId := c.GetInt("id")
	release, ok, err := service.AcquireLogExportLock(userId)
	if err != nil {
		common.SysError(common.LogText("failed to lock log export: %s", err.Error()))
		common.ApiErrorT(c, "Failed to export logs")
		return
	}
	if !ok {
		common.ApiErrorT(c, "Another log export is running. Try again after it finishes.")
		return
	}
	defer release()

	c.Header("Content-Type", "application/x-ndjson; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	count := 0
	writeLog := func(log *model.Log) error {
		line, err := common.Marshal(log)
		if err != nil {
			return err
		}
		if _, err := c.Writer.Write(append(line, '\n')); err != nil {
			return err
		}
		count++
		return nil
	}
	ctx := c.Request.Context()
	if all {
		err = model.StreamAllLogs(ctx, filter, c.GetInt("role"), writeLog)
	} else {
		err = model.StreamUserLogs(ctx, userId, filter, writeLog)
	}
	if ctx.Err() != nil {
		return
	}
	// The status is already sent, so the result object becomes the last line.
	if err != nil {
		common.SysError(common.LogText("failed to export logs: %s", err.Error()))
		common.ApiErrorT(c, "Failed to export logs")
		return
	}
	common.ApiSuccess(c, gin.H{"count": count})
}

// Deprecated: SearchAllLogs 已废弃，前端未使用该接口。
func SearchAllLogs(c *gin.Context) {
	common.ApiErrorT(c, "This API is deprecated")
}

// Deprecated: SearchUserLogs 已废弃，前端未使用该接口。
func SearchUserLogs(c *gin.Context) {
	common.ApiErrorT(c, "This API is deprecated")
}

func GetLogByKey(c *gin.Context) {
	tokenId := c.GetInt("token_id")
	if tokenId == 0 {
		common.ApiErrorI18n(c, i18n.MsgTokenInvalid)
		return
	}
	logs, err := model.GetLogByTokenId(tokenId)
	if err != nil {
		c.JSON(200, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(200, gin.H{
		"success": true,
		"message": "",
		"data":    logs,
	})
}

func GetLogsStat(c *gin.Context) {
	filter, err := logFilterFromQuery(c, true)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, err)
		return
	}
	stat, err := model.SumUsedQuota(filter)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"quota": stat.Quota,
			"rpm":   stat.Rpm,
			"tpm":   stat.Tpm,
		},
	})
	return
}

func GetLogsSelfStat(c *gin.Context) {
	filter, err := logFilterFromQuery(c, false)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusUnauthorized, err)
		return
	}
	role := c.GetInt("role")
	maxRange := operation_setting.GetLogSetting().MaxRangeSeconds()
	defaultStart := filter.StartTimestamp == 0 && role < common.RoleAdminUser
	if role < common.RoleAdminUser {
		if filter.StartTimestamp, err = limitLogTimeRangeWithMax(filter.StartTimestamp, filter.EndTimestamp, maxRange); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	quotaNum, err := service.GetSelfLogStat(c.Request.Context(), filter, role, maxRange, defaultStart)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(200, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"quota": quotaNum.Quota,
			"rpm":   quotaNum.Rpm,
			"tpm":   quotaNum.Tpm,
		},
	})
	return
}
