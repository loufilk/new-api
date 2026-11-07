package controller

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requestNewUserBenefitSettings(t *testing.T, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("id", 7)
	c.Request = httptest.NewRequest(method, "/api/option/new-user-benefit", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	if method == http.MethodGet {
		GetNewUserBenefitSettings(c)
	} else {
		UpdateNewUserBenefitSettings(c)
	}
	return recorder
}

func TestNewUserBenefitSettingsRejectInvalidPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{
		`{}`,
		`{"enabled":true,"pay_amount_cny":"0.10","credit_amount_cny":"1.00"}`,
		`{"enabled":null,"pay_amount_cny":"0.10","credit_amount_cny":"1.00","version":0}`,
		`{"enabled":true,"pay_amount_cny":0.1,"credit_amount_cny":"1.00","version":0}`,
		`{"enabled":true,"pay_amount_cny":"0.001","credit_amount_cny":"1.00","version":0}`,
		`{"enabled":true,"pay_amount_cny":"2.00","credit_amount_cny":"1.00","version":0}`,
		`{"enabled":true,"pay_amount_cny":"0.10","credit_amount_cny":"1.00","version":-1}`,
		`{"enabled":true,"pay_amount_cny":"0.10","credit_amount_cny":"1.00","version":0,"paid_quota":1000000}`,
	} {
		recorder := requestNewUserBenefitSettings(t, http.MethodPut, body)
		assert.Equal(t, http.StatusBadRequest, recorder.Code, body)
		assert.Contains(t, recorder.Body.String(), `"code":"new_user_benefit_invalid_config"`)
	}
}

func TestNewUserBenefitSettingsPostgresSaveReadAndStaleForm(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires an owned PostgreSQL 18.6 TEST_POSTGRES_DSN fixture")
	}
	db := setupWalletDatabase(t, "postgres", dsn)
	require.NoError(t, db.AutoMigrate(&model.NewUserBenefitConfig{}, &model.NewUserBenefitConfigAudit{}))
	get := requestNewUserBenefitSettings(t, http.MethodGet, "")
	require.Equal(t, http.StatusOK, get.Code)
	var response struct {
		Success bool                           `json:"success"`
		Data    newUserBenefitSettingsResponse `json:"data"`
	}
	require.NoError(t, common.Unmarshal(get.Body.Bytes(), &response))
	assert.Equal(t, newUserBenefitSettingsResponse{PayAmountCNY: "0.10", CreditAmountCNY: "1.00"}, response.Data)

	put := requestNewUserBenefitSettings(t, http.MethodPut, `{"enabled":true,"pay_amount_cny":"0.2","credit_amount_cny":"2","version":0}`)
	require.Equal(t, http.StatusOK, put.Code)
	require.NoError(t, common.Unmarshal(put.Body.Bytes(), &response))
	require.True(t, response.Success)
	assert.Equal(t, newUserBenefitSettingsResponse{Enabled: true, PayAmountCNY: "0.20", CreditAmountCNY: "2.00", Version: 1}, response.Data)
	stale := requestNewUserBenefitSettings(t, http.MethodPut, `{"enabled":false,"pay_amount_cny":"0.10","credit_amount_cny":"1.00","version":0}`)
	assert.Equal(t, http.StatusConflict, stale.Code)
	assert.Contains(t, stale.Body.String(), `"code":"new_user_benefit_config_changed"`)
	get = requestNewUserBenefitSettings(t, http.MethodGet, "")
	require.NoError(t, common.Unmarshal(get.Body.Bytes(), &response))
	assert.Equal(t, newUserBenefitSettingsResponse{Enabled: true, PayAmountCNY: "0.20", CreditAmountCNY: "2.00", Version: 1}, response.Data)
}
