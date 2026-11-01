package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// UnbindGoogleOAuth requires an operation-bound fresh proof and preserves another
// enabled login method under the same user/session locks as the credential change.
func UnbindGoogleOAuth(c *gin.Context) {
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok {
		writeSecurityOperationError(c, service.ErrAuthTokenInvalid)
		return
	}
	succeeded, notificationFailed := false, false
	defer func() {
		recordUserSecurityAudit(c, identity.UserID, "user.binding_unbind", map[string]any{"provider": "google", "success": succeeded, "notification_failed": notificationFailed})
	}()
	context, err := common.Marshal(service.AccountUnbindingContext{Provider: "google"})
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	if middleware.RequireSecurityProof(c, service.VerificationOperation{Scope: service.VerificationScopeAccountUnbind, Context: context}) == nil {
		return
	}
	if err := service.UnbindAccountGoogle(identity); err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	succeeded = true
	user, err := model.GetUserById(identity.UserID, false)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	notificationFailed = service.NotifyAccountSecurityChange(service.AccountMailLanguage(c, user.Id), user.Email, i18n.MsgSecurityEventLoginUnlinked) != nil
	bundle, err := service.AdvanceCurrentSessionToUserVersion(identity, "google_unlinked")
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	data := authRotationData(bundle)
	data["notification_warning"] = notificationFailed
	common.ApiSuccessT(c, "Unbound successfully", data)
}
