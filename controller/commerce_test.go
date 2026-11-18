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

func TestCommercePostgresRevokedPermissionCannotCommitManagementWrites(t *testing.T) {
	if groupRenameControllerIsolatedProcess(t) {
		return
	}
	f := setupInterceptionControllerTest(t)
	require.NoError(t, i18n.Init())
	require.NoError(t, f.db.AutoMigrate(append(model.CommerceModels(), &model.TopUp{})...))
	config := model.DefaultCommerceConfig()
	config.InvoiceEnabled = true
	storedConfig, err := model.SaveCommerceConfig(context.Background(), f.root.Id, config)
	require.NoError(t, err)
	order := model.TopUp{UserId: f.user.Id, TradeNo: "commerce-revoked-grant", Status: common.TopUpStatusSuccess, PaidCurrency: "CNY", PaidCurrencyExponent: 2, PaidAmountMinor: 12500, CompleteTime: common.GetTimestamp()}
	require.NoError(t, f.db.Create(&order).Error)
	invoice, err := model.CreateInvoiceApplication(context.Background(), f.user.Id, "revoked-invoice", model.InvoiceCreate{OrderIDs: []int{order.Id}, Kind: "general", TitleType: "individual", Title: "Example buyer", Email: "invoice@example.com"})
	require.NoError(t, err)
	partner, err := model.CreatePartnerApplication(context.Background(), f.user.Id, "revoked-partner", model.PartnerCreate{SiteName: "Example API", SiteURL: "https://supplier.example.com", ContactName: "Supplier", ContactEmail: "partner@example.com", Description: "API supply and availability details", Quotes: []model.PartnerQuote{{Group: "Standard", Models: "example-model", Rate: "0.35"}}})
	require.NoError(t, err)
	config.InvoiceEnabled, config.Revision = false, storedConfig.Revision
	configBody, err := common.Marshal(config)
	require.NoError(t, err)
	for _, tc := range []struct {
		name       string
		permission authz.Permission
		id         int64
		body       string
		handler    gin.HandlerFunc
	}{
		{"settings", authz.CommerceSettingsUpdate, 0, string(configBody), UpdateCommerceConfig},
		{"invoice", authz.CommerceInvoiceReview, invoice.ID, `{"expected_revision":1,"action":"approve"}`, ReviewInvoiceApplication},
		{"partner", authz.CommercePartnerReview, partner.ID, `{"expected_revision":1,"action":"approve","note":"Accepted"}`, ReviewPartnerApplication},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, authz.SetUserPermissions(f.admin.Id, authz.PermissionsMap{tc.permission.Resource: {tc.permission.Action: true}}))
			require.True(t, authz.Can(f.admin.Id, f.admin.Role, tc.permission))
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			body := &groupGuardBodyReader{reader: strings.NewReader(tc.body), beforeRead: func() {
				// The initial controller permission check already succeeded; revoke
				// before the write transaction acquires the authoritative actor lock.
				require.NoError(t, authz.SetUserPermissions(f.admin.Id, authz.PermissionsMap{tc.permission.Resource: {tc.permission.Action: false}}))
			}}
			c.Request = httptest.NewRequest(http.MethodPost, "/api/commerce-test", body)
			c.Set("id", f.admin.Id)
			c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(tc.id)}}
			tc.handler(c)
			assert.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
		})
	}
	afterConfig, err := model.GetCommerceConfig(context.Background())
	require.NoError(t, err)
	assert.Equal(t, storedConfig, afterConfig)
	afterInvoice, err := model.GetInvoiceApplication(context.Background(), f.user.Id, invoice.ID, false)
	require.NoError(t, err)
	assert.Equal(t, invoice, afterInvoice)
	afterPartner, err := model.GetPartnerApplication(context.Background(), f.user.Id, partner.ID, false)
	require.NoError(t, err)
	assert.Equal(t, partner, afterPartner)
}

func TestCommercePostgresInvoiceOverviewUsesAuthenticatedOwnerAndFullTotals(t *testing.T) {
	if groupRenameControllerIsolatedProcess(t) {
		return
	}
	f := setupInterceptionControllerTest(t)
	require.NoError(t, i18n.Init())
	require.NoError(t, f.db.AutoMigrate(append(model.CommerceModels(), &model.TopUp{}, &model.Redemption{})...))
	config := model.DefaultCommerceConfig()
	config.InvoiceEnabled, config.InvoiceMaxOrders = true, 1
	_, err := model.SaveCommerceConfig(context.Background(), f.root.Id, config)
	require.NoError(t, err)
	orders := []model.TopUp{
		{UserId: f.user.Id, TradeNo: "overview-first", Status: common.TopUpStatusSuccess, PaidCurrency: "CNY", PaidCurrencyExponent: 2, PaidAmountMinor: 12500, CompleteTime: common.GetTimestamp()},
		{UserId: f.user.Id, TradeNo: "overview-second", Status: common.TopUpStatusSuccess, PaidCurrency: "CNY", PaidCurrencyExponent: 2, PaidAmountMinor: 25000, CompleteTime: common.GetTimestamp()},
		{UserId: f.admin.Id, TradeNo: "overview-other-user", Status: common.TopUpStatusSuccess, PaidCurrency: "CNY", PaidCurrencyExponent: 2, PaidAmountMinor: 99999, CompleteTime: common.GetTimestamp()},
	}
	require.NoError(t, f.db.Create(&orders).Error)
	response := commerceControllerRequest(f.user.Id, 0, "", "", GetEligibleInvoiceOrders)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var overview struct {
		Success bool `json:"success"`
		Data    struct {
			Items   []model.InvoiceOrder `json:"items"`
			Summary model.InvoiceSummary `json:"summary"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &overview))
	assert.True(t, overview.Success)
	assert.Len(t, overview.Data.Items, 2)
	assert.Equal(t, model.InvoiceSummaryAmount{AmountMinor: "37500", Count: 2}, overview.Data.Summary.Available)
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	assert.NotContains(t, response.Body.String(), "overview-other-user")
	body := fmt.Sprintf(`{"order_ids":[%d],"category":"software_development","kind":"general","title_type":"individual","title":"Example buyer","email":"invoice@example.com"}`, orders[0].Id)
	created := commerceControllerRequest(f.user.Id, 0, body, "overview-create", CreateInvoiceApplication)
	require.Equal(t, http.StatusOK, created.Code, created.Body.String())
	assert.Contains(t, created.Body.String(), `"category":"software_development"`)
	response = commerceControllerRequest(f.user.Id, 0, "", "", GetEligibleInvoiceOrders)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &overview))
	assert.Equal(t, model.InvoiceSummaryAmount{AmountMinor: "25000", Count: 1}, overview.Data.Summary.Available)
	assert.Equal(t, model.InvoiceSummaryAmount{AmountMinor: "12500", Count: 1}, overview.Data.Summary.Processing)
}
