package controller

import (
	"errors"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

func GetModelSquareOrderOptions(c *gin.Context) {
	order, err := model.ReadModelSquareOrder(c.Request.Context())
	if err != nil {
		common.SysError(common.MaskSensitiveLogInfo(err.Error()))
		common.ApiErrorStatus(c, http.StatusInternalServerError, common.NewMessage("Failed to load settings"))
		return
	}
	common.ApiSuccess(c, order)
}

func PatchModelSquareOrderOptions(c *gin.Context) {
	invalid := common.NewMessage("Invalid model square order setting")
	var payload map[string]map[string]common.RawMessage
	reader := http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	if err := common.DecodeJsonBody(reader, &payload); err != nil || len(payload) != 2 || payload["changes"] == nil || payload["expected"] == nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, invalid)
		return
	}
	changes, err := modelSquareOrderValues(payload["changes"])
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, invalid)
		return
	}
	expected, err := modelSquareOrderValues(payload["expected"])
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, invalid)
		return
	}
	if len(changes) != len(expected) {
		common.ApiErrorStatus(c, http.StatusBadRequest, invalid)
		return
	}
	for key := range changes {
		if _, ok := expected[key]; !ok {
			common.ApiErrorStatus(c, http.StatusBadRequest, invalid)
			return
		}
	}
	err = model.UpdateModelSquareOrderOptions(c.Request.Context(), changes, expected)
	if errors.Is(err, model.ErrModelSquareOrderChanged) {
		common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage("Could not load or save the settings. Reload them before retrying to avoid overwriting another administrator’s changes."))
		return
	}
	if err != nil {
		common.SysError(common.MaskSensitiveLogInfo(err.Error()))
		common.ApiErrorStatus(c, http.StatusInternalServerError, common.NewMessage("Failed to update setting"))
		return
	}
	// Preserve the legacy option audit without recording model names or values.
	for _, key := range slices.Sorted(maps.Keys(changes)) {
		recordManageAudit(c, "option.update", map[string]any{"key": "model_square_setting." + key})
	}
	common.ApiSuccess(c, nil)
}

// Keep the editor payload typed while retaining the existing option rows and
// wire format for clients that still use the older single-option endpoint.
func modelSquareOrderValues(fields map[string]common.RawMessage) (map[string]string, error) {
	invalid := common.NewMessage("Invalid model square order setting")
	if len(fields) == 0 || len(fields) > 4 {
		return nil, invalid
	}
	values := make(map[string]string, len(fields))
	for key, raw := range fields {
		if strings.TrimSpace(string(raw)) == "null" {
			return nil, invalid
		}
		switch key {
		case "sort_mode", "sort_metric":
			var value string
			if err := common.Unmarshal(raw, &value); err != nil {
				return nil, invalid
			}
			values[key] = value
		case "sort_days":
			var days int
			if err := common.Unmarshal(raw, &days); err != nil {
				return nil, invalid
			}
			values[key] = strconv.Itoa(days)
		case "sort_models":
			var models []*string
			if err := common.Unmarshal(raw, &models); err != nil {
				return nil, invalid
			}
			for _, name := range models {
				if name == nil {
					return nil, invalid
				}
			}
			encoded, err := common.Marshal(models)
			if err != nil {
				return nil, invalid
			}
			values[key] = string(encoded)
		default:
			return nil, invalid
		}
		if err := operation_setting.ValidateModelSquareOption("model_square_setting."+key, values[key]); err != nil {
			return nil, err
		}
	}
	return values, nil
}
