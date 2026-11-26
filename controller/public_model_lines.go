package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func GetPublicModelLineSettings(c *gin.Context) {
	guard, ok := beginPublicGroupConfigRead(c)
	if !ok {
		return
	}
	defer guard.Close()
	groups, err := model.ReadPublicModelLineRecords(c.Request.Context())
	if err != nil {
		groupConfigReadFailure(c)
		return
	}
	if finishControllerGroupConfigRead(c, guard) {
		common.ApiSuccess(c, model.PublicModelLineConfig{Revision: guard.Version, Groups: groups})
	}
}

func PutPublicModelLineSettings(c *gin.Context) {
	identity, ok := middleware.GetStepUpIdentity(c)
	if !ok || identity.UserID != c.GetInt("id") {
		groupCatalogError(c, model.ErrGroupRenamePermission)
		return
	}
	var request struct {
		Revision *int64                                   `json:"revision"`
		Groups   map[string]model.PublicModelLineSettings `json:"groups"`
	}
	if err := common.DecodeJsonStrict(http.MaxBytesReader(c.Writer, c.Request.Body, 128<<10), &request); err != nil || request.Revision == nil || request.Groups == nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, model.ErrInvalidPublicModelLines)
		return
	}
	if !common.IM21RolloutActive() {
		im21RolloutLocked(c)
		return
	}
	guard, ok := beginPublicGroupConfigRead(c)
	if !ok {
		return
	}
	defer guard.Close()
	configured, err := model.ReadPublicModelLineRecords(c.Request.Context())
	if err != nil || !finishControllerGroupConfigRead(c, guard) {
		if err != nil {
			groupConfigReadFailure(c)
		}
		return
	}
	if guard.Version != *request.Revision {
		groupCatalogError(c, model.ErrGroupConfigChanged)
		return
	}
	changed := make([]string, 0, len(request.Groups))
	for group, entry := range request.Groups {
		if old := configured[group]; old.ID == "" || !entry.Equal(old.PublicModelLineSettings) {
			changed = append(changed, group)
		}
	}
	liveModels, err := model.LivePublicLineModelMembership(c.Request.Context(), changed)
	if err != nil {
		groupConfigReadFailure(c)
		return
	}
	snapshot := model.GetPricingCatalogSnapshot()
	for _, group := range changed {
		entry := request.Groups[group]
		alias := map[string]string{group: group}
		foundModel, foundRepresentative := false, entry.RepresentativeModel == ""
		for _, row := range snapshot.Rows {
			if row.ModelName == "stealth/pixel-canary" || !liveModels[group][row.ModelName] {
				continue
			}
			_, support := row.ProjectEndpointGroups(alias)
			if len(support[group]) == 0 {
				continue
			}
			foundModel = true
			if row.ModelName == entry.RepresentativeModel {
				foundRepresentative = true
			}
			if foundRepresentative {
				break
			}
		}
		if !foundModel || !foundRepresentative {
			common.ApiErrorStatus(c, http.StatusBadRequest, model.ErrInvalidPublicModelLines)
			return
		}
	}
	result, err := model.UpdatePublicModelLines(c.Request.Context(), *request.Revision, request.Groups, identity)
	if err == model.ErrInvalidPublicModelLines {
		common.ApiErrorStatus(c, http.StatusBadRequest, err)
		return
	}
	if err != nil {
		groupCatalogError(c, err)
		return
	}
	recordManageAudit(c, "option.public_model_lines.update", map[string]any{"revision": result.Revision, "count": len(result.Groups)})
	common.ApiSuccess(c, result)
}
