package controller

import (
	"bytes"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCustomerInvoiceHTTPStrictJSON(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body string
		want                    int
	}{
		{"valid reason", "application/json; charset=utf-8", `{"reason":"synthetic"}`, http.StatusOK},
		{"unknown identity field", "application/json", `{"reason":"synthetic","user_id":55}`, http.StatusBadRequest},
		{"unknown field not exposed", "application/json", `{"synthetic-private":1}`, http.StatusBadRequest},
		{"trailing JSON", "application/json", `{"reason":"synthetic"}{}`, http.StatusBadRequest},
		{"oversize", "application/json", strings.Repeat(" ", invoiceJSONLimit+1), http.StatusRequestEntityTooLarge},
		{"unsupported media", "text/plain", `{}`, http.StatusUnsupportedMediaType},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			c.Request.ContentLength = -1 // also check the limit with unknown size
			c.Request.Header.Set("Content-Type", tc.contentType)
			var dst struct {
				Reason string `json:"reason"`
			}
			ok := readCustomerInvoiceJSON(c, &dst)
			require.Equal(t, tc.want == http.StatusOK, ok)
			require.Equal(t, tc.want, response.Code)
			require.NotContains(t, response.Body.String(), "synthetic-private")
		})
	}
}

func TestCustomerInvoiceHTTPFinanceUpload(t *testing.T) {
	// Structural stand-in for the externally issued artifact. No real tax data.
	pdf := []byte("%PDF-1.4\nxref\n0 1\n0000000000 65535 f \ntrailer\n<< /Size 1 >>\nstartxref\n9\n%%EOF\n")
	for _, tc := range []struct {
		name            string
		number, confirm string
		file            []byte
		extra, epilogue string
		want            error
	}{
		{name: "all three parts", number: "DEMO-1", confirm: "true", file: pdf},
		{name: "missing attestation", number: "DEMO-1", file: pdf, want: model.ErrCustomerInvoiceInvalid},
		{name: "false attestation", number: "DEMO-1", confirm: "false", file: pdf, want: model.ErrCustomerInvoiceInvalid},
		{name: "missing number", confirm: "true", file: pdf, want: model.ErrCustomerInvoiceInvalid},
		{name: "missing file", number: "DEMO-1", confirm: "true", want: model.ErrCustomerInvoiceInvalid},
		{name: "duplicate attestation", number: "DEMO-1", confirm: "true", file: pdf, extra: "confirm_payment_verified", want: model.ErrCustomerInvoiceInvalid},
		{name: "unexpected field", number: "DEMO-1", confirm: "true", file: pdf, extra: "user_id", want: model.ErrCustomerInvoiceInvalid},
		{name: "bad file", number: "DEMO-1", confirm: "true", file: []byte("not a PDF"), want: model.ErrCustomerInvoicePDFInvalid},
		{name: "large file", number: "DEMO-1", confirm: "true", file: make([]byte, model.CustomerInvoicePDFMaxBytes+1), want: errInvoiceBodyLimit},
		{name: "chunked epilogue", number: "DEMO-1", confirm: "true", file: pdf, epilogue: strings.Repeat(" ", model.CustomerInvoicePDFMaxBytes+(64<<10)), want: errInvoiceBodyLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body bytes.Buffer
			w := multipart.NewWriter(&body)
			if tc.file != nil {
				part, err := w.CreateFormFile("file", "never-publish-original-name.pdf")
				require.NoError(t, err)
				_, err = part.Write(tc.file)
				require.NoError(t, err)
			}
			if tc.number != "" {
				require.NoError(t, w.WriteField("invoice_number", tc.number))
			}
			if tc.confirm != "" {
				require.NoError(t, w.WriteField("confirm_payment_verified", tc.confirm))
			}
			if tc.extra != "" {
				require.NoError(t, w.WriteField(tc.extra, "other"))
			}
			require.NoError(t, w.Close())
			body.WriteString(tc.epilogue)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/", &body)
			c.Request.Header.Set("Content-Type", w.FormDataContentType())
			c.Request.ContentLength = -1
			number, result, err := readCustomerInvoiceIssue(c)
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
				require.Empty(t, result.Data)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.number, number)
			require.Equal(t, pdf, result.Data)
		})
	}
}

func TestCustomerInvoiceHTTPErrorNeverLeaksRawText(t *testing.T) {
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	customerInvoiceError(c, errors.New("storage failed with synthetic private taxpayer data"))
	require.Equal(t, http.StatusInternalServerError, response.Code)
	require.NotContains(t, response.Body.String(), "synthetic private")
	require.Contains(t, response.Body.String(), "INVOICE_UNAVAILABLE")
}

func TestCustomerInvoiceHTTPReadOnlyWhenDisabled(t *testing.T) {
	t.Setenv("CUSTOMER_INVOICE_ENABLED", "false")
	t.Setenv("CUSTOMER_INVOICE_EPAY_MERCHANT_SCOPE", "non-public-test-scope")
	t.Setenv("CUSTOMER_INVOICE_WALLET_QUOTA_PER_CNY_CENT", "")
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	GetCustomerInvoiceConfig(c)
	require.JSONEq(t, `{"success":true,"message":"","data":{"enabled":false}}`, response.Body.String())
	response = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	SubmitCustomerInvoice(c)
	require.Equal(t, http.StatusForbidden, response.Code)
	require.NotContains(t, response.Body.String(), "non-public-test-scope")
}

func TestCustomerInvoiceHTTPWalletCapabilityRequiresMerchantRateAndCurrentCompliance(t *testing.T) {
	t.Setenv("CUSTOMER_INVOICE_ENABLED", "false") // intake switch is separate
	t.Setenv("CUSTOMER_INVOICE_EPAY_MERCHANT_SCOPE", "demo-merchant")
	t.Setenv("CUSTOMER_INVOICE_WALLET_QUOTA_PER_CNY_CENT", "500")
	setting := operation_setting.GetPaymentSetting()
	confirmed, terms := setting.ComplianceConfirmed, setting.ComplianceTermsVersion
	t.Cleanup(func() {
		setting.ComplianceConfirmed, setting.ComplianceTermsVersion = confirmed, terms
	})
	setting.ComplianceConfirmed, setting.ComplianceTermsVersion = true, "old-terms"
	require.Zero(t, invoiceWalletPaymentAvailable())
	setting.ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
	require.EqualValues(t, 500, invoiceWalletPaymentAvailable())
	methods, rate := customerInvoiceFeeMethods()
	require.EqualValues(t, 500, rate)
	require.Contains(t, methods, customerInvoiceFeeMethod{ID: "paid_wallet", Name: "Paid balance", Rail: "wallet"})
	t.Setenv("CUSTOMER_INVOICE_EPAY_MERCHANT_SCOPE", "")
	methods, rate = customerInvoiceFeeMethods()
	require.Zero(t, rate)
	require.Empty(t, methods)
}

func TestCustomerInvoiceHTTPSettingsRequireExplicitCompleteReplacement(t *testing.T) {
	const ordinary = `"ordinary":{"enabled":true,"fee_enabled":false,"fee_rate_bps":0,"tax_rate_bps":0}`
	const special = `"special":{"enabled":false,"fee_enabled":false,"fee_rate_bps":0,"tax_rate_bps":0}`
	for _, body := range []string{
		`{}`, `null`, `{"version":0,` + ordinary + `}`,
		`{"version":0,` + special + `}`, `{` + ordinary + `,` + special + `}`,
		`{"version":null,` + ordinary + `,` + special + `}`,
		`{"version":0,` + ordinary + `,` + special + `,"categories":null}`,
		`{"version":0,` + ordinary + `,` + special + `,"categories":[{"id":"one","name":"One"}]}`,
		`{"version":0,` + ordinary + `,` + special + `,"categories":[{"id":"one","name":"One","enabled":false,"unexpected":true}]}`,
		`{"version":0,` + ordinary + `,` + special + `,"updated_by":123}`,
		`{"version":0,` + ordinary + `,"special":{"enabled":false,"fee_enabled":null,"fee_rate_bps":0,"tax_rate_bps":0}}`,
		`{"version":0,` + ordinary + `,"special":{"enabled":false,"fee_enabled":false,"fee_rate_bps":1.2,"tax_rate_bps":0}}`,
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPut, "/api/admin/invoice/settings", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		UpdateAdminCustomerInvoiceSettings(c)
		require.Equal(t, http.StatusBadRequest, w.Code, body)
		require.Contains(t, w.Body.String(), "INVOICE_INVALID")
	}
}

func TestCustomerInvoiceHTTPAdminStatusFilter(t *testing.T) {
	for _, tc := range []struct {
		filter        string
		admin, expect bool
	}{
		{"refund_review", false, false},
		{"refund_review", true, true},
		{"fee_refund_review", false, false},
		{"fee_refund_review", true, true},
		{"awaiting_payment", true, true},
		{"awaiting_payment", false, true},
	} {
		response := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(response)
		c.Request = httptest.NewRequest(http.MethodGet, "/?status="+tc.filter, nil)
		q, ok := customerInvoicePageQuery(c, true, tc.admin)
		require.Equal(t, tc.expect, ok)
		if tc.expect {
			require.Equal(t, tc.filter, q.Status)
		} else {
			require.Equal(t, http.StatusBadRequest, response.Code)
		}
	}
}

func TestCustomerInvoiceHTTPClaimBodyAndReleaseConfirmation(t *testing.T) {
	// Non-empty body cannot select an actor, owner, or another applicant.
	for _, n := range []int64{-1, 2} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Params = gin.Params{{Key: "id", Value: "1"}}
		c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}"))
		c.Request.ContentLength = n
		ClaimCustomerInvoice(c)
		require.Equal(t, http.StatusBadRequest, w.Code)
	}
	// Every invalid case must stop at the HTTP boundary before any DB access.
	for _, body := range []string{
		`{}`, `null`, `{"confirm_not_issued":false}`, `{"confirm_not_issued":null}`,
		`{"confirm_not_issued":"true"}`, `{"confirm_not_issued":1}`,
		`{"confirm_not_issued":false,"confirm_not_issued":true}`,
		`{"confirm_not_issued":true,"confirm_not_issued":true}`,
		`{"confirm_not_issued":true,"user_id":9}`,
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Params = gin.Params{{Key: "id", Value: "1"}}
		c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		ReleaseCustomerInvoiceClaim(c)
		require.Equal(t, http.StatusBadRequest, w.Code, body)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"confirm_not_issued": true}`))
	c.Request.Header.Set("Content-Type", "application/json")
	var input struct {
		Confirm invoiceReleaseConfirmation `json:"confirm_not_issued"`
	}
	require.True(t, readCustomerInvoiceJSON(c, &input))
	require.True(t, bool(input.Confirm))
}

func TestCustomerInvoiceHTTPAdminAndCustomerCategoriesPostgres(t *testing.T) {
	db := setupNewUserBenefitControllerPostgres(t)
	require.NoError(t, db.AutoMigrate(model.CustomerInvoiceModels()...))
	t.Setenv("CUSTOMER_INVOICE_ENABLED", "true")
	t.Setenv("CUSTOMER_INVOICE_EPAY_MERCHANT_SCOPE", "invoice-category-fixture")
	user := createNewUserBenefitControllerUser(t, db, "categories-root", false)
	require.NoError(t, db.Model(&user).Update("role", common.RoleRootUser).Error)
	invoke := func(handler gin.HandlerFunc, method, path, input string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("id", user.Id)
		c.Request = httptest.NewRequest(method, path, strings.NewReader(input))
		c.Request.Header.Set("Content-Type", "application/json")
		handler(c)
		return w
	}
	read := func(handler gin.HandlerFunc, path string) (int64, []model.CustomerInvoiceCategory) {
		w := invoke(handler, http.MethodGet, path, "")
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var result struct {
			Success bool
			Data    struct {
				Version    int64
				Categories []model.CustomerInvoiceCategory
			}
		}
		require.NoError(t, common.Unmarshal(w.Body.Bytes(), &result))
		require.True(t, result.Success)
		require.NotNil(t, result.Data.Categories, "the absence of choices must be [] not null")
		return result.Data.Version, result.Data.Categories
	}
	const admin = "/api/admin/invoice/settings"
	const customer = "/api/invoice/config"
	const rateFields = `"ordinary":{"enabled":true,"fee_enabled":false,"fee_rate_bps":0,"tax_rate_bps":0},"special":{"enabled":true,"fee_enabled":false,"fee_rate_bps":0,"tax_rate_bps":0}`
	version, fresh := read(GetAdminCustomerInvoiceSettings, admin)
	require.Len(t, fresh, 2)
	publicVersion, public := read(GetCustomerInvoiceConfig, customer)
	require.Equal(t, fresh, public)
	require.Equal(t, version, publicVersion)
	const choices = `[{"id":"one","name":"First","enabled":true},{"id":"two","name":"Disabled choice","enabled":false},{"id":"three","name":"Third","enabled":true}]`
	write := func(v int64, extra string) *httptest.ResponseRecorder {
		return invoke(UpdateAdminCustomerInvoiceSettings, http.MethodPut, admin, fmt.Sprintf(`{"version":%d,%s%s}`, v, rateFields, extra))
	}
	w := write(version, `,"categories":`+choices)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "Disabled choice")
	v, all := read(GetAdminCustomerInvoiceSettings, admin)
	require.Greater(t, v, version)
	require.Len(t, all, 3)
	publicVersion, public = read(GetCustomerInvoiceConfig, customer)
	require.Equal(t, v, publicVersion)
	require.Equal(t, []model.CustomerInvoiceCategory{all[0], all[2]}, public)
	require.Equal(t, http.StatusConflict, write(version, `,"categories":[]`).Code)
	require.Equal(t, http.StatusOK, write(v, "").Code, "pre-category PUT preserves settings and revision")
	version, stored := read(GetAdminCustomerInvoiceSettings, admin)
	require.Equal(t, v, version)
	require.Equal(t, all, stored)
	w = write(version, `,"categories":[]`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	publicVersion, public = read(GetCustomerInvoiceConfig, customer)
	require.Greater(t, publicVersion, version)
	require.Empty(t, public)
}

func TestCustomerInvoiceHTTPAdminCenterSwitchAndLegacyClientPostgres(t *testing.T) {
	db := setupNewUserBenefitControllerPostgres(t)
	require.NoError(t, db.AutoMigrate(model.CustomerInvoiceModels()...))
	t.Setenv("CUSTOMER_INVOICE_ENABLED", "true")
	t.Setenv("CUSTOMER_INVOICE_EPAY_MERCHANT_SCOPE", "invoice-center-fixture")
	t.Setenv("CUSTOMER_INVOICE_WALLET_QUOTA_PER_CNY_CENT", "")
	root := createNewUserBenefitControllerUser(t, db, "invoice-center-settings", false)
	require.NoError(t, db.Model(&root).Update("role", common.RoleRootUser).Error)
	invoke := func(handler gin.HandlerFunc, method, path, input string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("id", root.Id)
		c.Request = httptest.NewRequest(method, path, strings.NewReader(input))
		c.Request.Header.Set("Content-Type", "application/json")
		handler(c)
		return w
	}
	const adminPath = "/api/admin/invoice/settings"
	const customerPath = "/api/invoice/config"
	const rateFields = `"ordinary":{"enabled":true,"fee_enabled":false,"fee_rate_bps":0,"tax_rate_bps":0},"special":{"enabled":true,"fee_enabled":false,"fee_rate_bps":0,"tax_rate_bps":0}`
	readSettings := func(handler gin.HandlerFunc, path string) (int64, bool, bool) {
		w := invoke(handler, http.MethodGet, path, "")
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var response struct {
			Data struct {
				Version       int64 `json:"version"`
				CenterEnabled bool  `json:"center_enabled"`
				Enabled       bool  `json:"enabled"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
		return response.Data.Version, response.Data.CenterEnabled, response.Data.Enabled
	}
	version, centerEnabled, _ := readSettings(GetAdminCustomerInvoiceSettings, adminPath)
	require.True(t, centerEnabled)
	_, _, intakeEnabled := readSettings(GetCustomerInvoiceConfig, customerPath)
	require.True(t, intakeEnabled)
	write := func(v int64, rates, center string) *httptest.ResponseRecorder {
		return invoke(UpdateAdminCustomerInvoiceSettings, http.MethodPut, adminPath, fmt.Sprintf(`{"version":%d,%s%s}`, v, rates, center))
	}
	w := write(version, rateFields, `,"center_enabled":false`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	version, centerEnabled, _ = readSettings(GetAdminCustomerInvoiceSettings, adminPath)
	require.False(t, centerEnabled)
	_, _, intakeEnabled = readSettings(GetCustomerInvoiceConfig, customerPath)
	require.False(t, intakeEnabled)

	previous := version
	w = write(version, strings.Replace(rateFields, `"tax_rate_bps":0`, `"tax_rate_bps":600`, 1), "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	version, centerEnabled, _ = readSettings(GetAdminCustomerInvoiceSettings, adminPath)
	require.Equal(t, previous+1, version, "the legacy client must actually append a revision without reopening intake")
	require.False(t, centerEnabled)
	w = write(version, rateFields, `,"center_enabled":true`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	_, centerEnabled, _ = readSettings(GetAdminCustomerInvoiceSettings, adminPath)
	require.True(t, centerEnabled)
}

func TestAdminInvoiceIntakeConfigurationStatus(t *testing.T) {
	base := model.CustomerInvoiceSettings{
		CenterEnabled: true,
		Ordinary:      model.CustomerInvoiceTypeSettings{Enabled: true},
		Categories:    model.CustomerInvoiceCategories{{ID: "qa", Name: "Synthetic", Enabled: true}},
	}
	for _, tc := range []struct {
		name, flag, scope, expected string
		mutate                      func(*model.CustomerInvoiceSettings)
	}{
		{name: "ready", scope: "synthetic-not-exposed", expected: "configured"},
		{name: "merchant missing", expected: "merchant_not_configured"},
		{name: "deployment off", flag: "false", scope: "synthetic-not-exposed", expected: "deployment_off"},
		{name: "center off", scope: "synthetic-not-exposed", expected: "center_off", mutate: func(s *model.CustomerInvoiceSettings) { s.CenterEnabled = false }},
		{name: "no invoice types", scope: "synthetic-not-exposed", expected: "no_invoice_types", mutate: func(s *model.CustomerInvoiceSettings) { s.Ordinary.Enabled = false }},
		{name: "no categories", scope: "synthetic-not-exposed", expected: "no_categories", mutate: func(s *model.CustomerInvoiceSettings) { s.Categories = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CUSTOMER_INVOICE_ENABLED", tc.flag)
			t.Setenv("CUSTOMER_INVOICE_EPAY_MERCHANT_SCOPE", tc.scope)
			settings := base
			if tc.mutate != nil {
				tc.mutate(&settings)
			}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			respondAdminInvoiceSettings(c, settings, nil)
			require.Equal(t, http.StatusOK, w.Code)
			require.Contains(t, w.Body.String(), `"intake_state":"`+tc.expected+`"`)
			require.NotContains(t, w.Body.String(), "synthetic-not-exposed")
		})
	}
}
