package controller

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func CreateLogCleanupSystemTask(c *gin.Context) {
	createLogCleanupSystemTask(c, false)
}

// Keep the receipt contract on a distinct route. An older server must reject
// a new client's write rather than accept it while silently dropping the ID.
func CreateLogCleanupSystemTaskWithReceipt(c *gin.Context) {
	createLogCleanupSystemTask(c, true)
}

func createLogCleanupSystemTask(c *gin.Context, requireReceipt bool) {
	targetTimestamp, cutoffErr := strconv.ParseInt(c.Query("target_timestamp"), 10, 64)
	if cutoffErr != nil || targetTimestamp <= 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "target timestamp is required",
			"code":    "invalid_log_cleanup_cutoff",
		})
		return
	}

	taskID := ""
	if requireReceipt {
		taskID = c.Query("task_id")
		rawID, hasPrefix := strings.CutPrefix(taskID, "systask_")
		parsed, err := uuid.Parse(rawID)
		if !hasPrefix || err != nil || parsed == uuid.Nil || parsed.String() != rawID {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "invalid_log_cleanup_task_id"})
			return
		}
	}
	task, err := service.StartLogCleanupTask(targetTimestamp, taskID)
	if errors.Is(err, service.ErrLogCleanupTaskConflict) {
		c.JSON(http.StatusConflict, gin.H{"success": false, "code": "log_cleanup_task_id_conflict"})
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if requireReceipt && task.TaskID != taskID {
		c.JSON(http.StatusConflict, gin.H{
			"success": false,
			"code":    "log_cleanup_already_running",
			"data":    task.ToResponse(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    task.ToResponse(),
	})
}

func GetCurrentSystemTask(c *gin.Context) {
	taskType := c.Query("type")
	if taskType == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "type is required",
		})
		return
	}

	task, err := model.GetActiveSystemTask(taskType)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if task == nil {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
			"data":    nil,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    task.ToResponse(),
	})
}

func ListSystemTasks(c *gin.Context) {
	var query struct {
		model.SystemTaskFilter
		Offset int `form:"offset" binding:"gte=0"`
		Limit  int `form:"limit"`
	}
	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid system task filters"})
		return
	}

	tasks, total, err := model.ListSystemTasks(query.SystemTaskFilter, query.Offset, query.Limit)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	responses := make([]model.SystemTaskResponse, 0, len(tasks))
	for _, task := range tasks {
		responses = append(responses, task.ToResponse())
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    responses,
		"total":   total,
	})
}

func DeleteSystemTaskHistory(c *gin.Context) {
	var filter model.SystemTaskFilter
	if err := c.ShouldBindQuery(&filter); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid system task filters"})
		return
	}
	deleted, err := model.DeleteSystemTaskHistory(filter)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    gin.H{"deleted_count": deleted},
	})
}

func GetSystemTask(c *gin.Context) {
	taskID := c.Param("task_id")
	if taskID == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "task id is required",
		})
		return
	}

	task, err := model.GetSystemTaskByTaskID(taskID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if task == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "task not found",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    task.ToResponse(),
	})
}
