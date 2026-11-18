package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func commerceControllerRequest(actor, id int, body, requestKey string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/commerce-test", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Idempotency-Key", requestKey)
	c.Set("id", actor)
	c.Set("role", common.RoleRootUser) // The persisted actor remains authoritative.
	c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(id)}}
	handler(c)
	return response
}

func TestCommercePostgresAPIReadWriteOwnershipAndPermissionBoundaries(t *testing.T) {
	if groupRenameControllerIsolatedProcess(t) {
		return
	}
	f := setupInterceptionControllerTest(t)
	require.NoError(t, i18n.Init())
	require.NoError(t, f.db.AutoMigrate(append(model.CommerceModels(), &model.TopUp{})...))
	config := model.DefaultCommerceConfig()
	config.InvoiceEnabled = true
	_, err := model.SaveCommerceConfig(context.Background(), f.root.Id, config)
	require.NoError(t, err)
	order := model.TopUp{UserId: f.user.Id, TradeNo: "commerce-controller-paid", Status: common.TopUpStatusSuccess, PaidCurrency: "CNY", PaidCurrencyExponent: 2, PaidAmountMinor: 12500, CompleteTime: common.GetTimestamp()}
	require.NoError(t, f.db.Create(&order).Error)
	body := fmt.Sprintf(`{"order_ids":[%d],"kind":"general","title_type":"company","title":"Example buyer","tax_id":"DEMO","email":"invoice@example.com","user_id":%d,"status":"issued"}`, order.Id, f.root.Id)
	response := commerceControllerRequest(f.user.Id, 0, body, "invoice-create-api", CreateInvoiceApplication)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	var created struct {
		Success bool                     `json:"success"`
		Data    model.InvoiceApplication `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &created))
	assert.Equal(t, f.user.Id, created.Data.UserID)
	assert.Equal(t, "submitted", created.Data.Status)
	assert.NotContains(t, response.Body.String(), "request_key")
	assert.NotContains(t, response.Body.String(), "request_hash")
	assert.Equal(t, http.StatusNotFound, commerceControllerRequest(f.admin.Id, int(created.Data.ID), "", "", GetInvoiceApplication).Code)
	assert.Equal(t, http.StatusForbidden, commerceControllerRequest(f.user.Id, 0, "", "", ListAdminInvoiceApplications).Code)
	require.NoError(t, authz.SetUserPermissions(f.admin.Id, authz.PermissionsMap{"commerce_invoices": {"read": true, "review": false}, "commerce_partners": {"read": false, "review": false}, "commerce_settings": {"read": true, "update": false}}))
	assert.Equal(t, http.StatusOK, commerceControllerRequest(f.admin.Id, int(created.Data.ID), "", "", GetAdminInvoiceApplication).Code)
	assert.Equal(t, http.StatusForbidden, commerceControllerRequest(f.admin.Id, int(created.Data.ID), `{"expected_revision":1,"action":"approve"}`, "", ReviewInvoiceApplication).Code)
	assert.Equal(t, http.StatusForbidden, commerceControllerRequest(f.user.Id, int(created.Data.ID), `{"expected_revision":1,"action":"approve"}`, "", CancelInvoiceApplication).Code)
	assert.Equal(t, http.StatusBadRequest, commerceControllerRequest(f.user.Id, 0, body, "", CreateInvoiceApplication).Code)
	assert.Equal(t, http.StatusBadRequest, commerceControllerRequest(f.user.Id, 0, "{} trailing", "another-api-key", CreateInvoiceApplication).Code)
	assert.Equal(t, http.StatusBadRequest, commerceControllerRequest(f.user.Id, 0, strings.Repeat("x", 128*1024+1), "body-limit-api", CreateInvoiceApplication).Code)
	assert.Equal(t, http.StatusForbidden, commerceControllerRequest(f.admin.Id, 0, "{}", "", UpdateCommerceConfig).Code)
	response = commerceControllerRequest(f.root.Id, int(created.Data.ID), `{"expected_revision":1,"action":"approve"}`, "", ReviewInvoiceApplication)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, http.StatusConflict, commerceControllerRequest(f.root.Id, int(created.Data.ID), `{"expected_revision":1,"action":"approve"}`, "", ReviewInvoiceApplication).Code)
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", f.root.Id).Update("status", common.UserStatusDisabled).Error)
	assert.Equal(t, http.StatusForbidden, commerceControllerRequest(f.root.Id, int(created.Data.ID), "", "", GetAdminInvoiceApplication).Code)
}
