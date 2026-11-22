package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type playgroundErrorPayload struct {
	Error struct {
		Message string          `json:"message"`
		Code    types.ErrorCode `json:"code"`
	} `json:"error"`
}

func setupPlaygroundControllerTestDB(t *testing.T, quota int) int {
	t.Helper()
	db := setupModelListControllerTestDB(t)
	user := model.User{
		Id:       2101,
		Username: "playground-quota-user",
		Password: "password",
		Group:    "default",
		Status:   common.UserStatusEnabled,
		Quota:    quota,
	}
	require.NoError(t, db.Create(&user).Error)
	return user.Id
}

func newPlaygroundChatRequest(t *testing.T, userId int) (*httptest.ResponseRecorder, *gin.Context) {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("id", userId)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/pg/chat/completions",
		strings.NewReader(`{"model":"zz-playground-test-model","messages":[{"role":"user","content":"hi"}]}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")
	return recorder, ctx
}

func decodePlaygroundErrorPayload(t *testing.T, recorder *httptest.ResponseRecorder) playgroundErrorPayload {
	t.Helper()
	var payload playgroundErrorPayload
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	return payload
}

// TestPlaygroundRejectsInsufficientUserQuota guards the wallet gate: a user
// whose wallet balance is zero or negative must get HTTP 403 with
// insufficient_user_quota before the request ever reaches the relay. Without
// the gate the same request would proceed into Relay and fail there with a
// different status/code (no channel or no pricing), so this exact response is
// also the proof that the relay was not entered.
func TestPlaygroundRejectsInsufficientUserQuota(t *testing.T) {
	tests := []struct {
		name  string
		quota int
	}{
		{name: "zero quota", quota: 0},
		{name: "negative quota", quota: -100},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			userId := setupPlaygroundControllerTestDB(t, test.quota)
			recorder, ctx := newPlaygroundChatRequest(t, userId)

			Playground(ctx)

			require.Equal(t, http.StatusForbidden, recorder.Code)
			payload := decodePlaygroundErrorPayload(t, recorder)
			assert.Equal(t, types.ErrorCodeInsufficientUserQuota, payload.Error.Code)
		})
	}
}

// TestPlaygroundAllowsPositiveQuotaPastWalletGate is the control case: a user
// with a positive balance must NOT be blocked by the wallet gate. The request
// is allowed to proceed into the relay, which then fails downstream (the test
// DB has no channel/pricing for the model), producing a different response.
func TestPlaygroundAllowsPositiveQuotaPastWalletGate(t *testing.T) {
	userId := setupPlaygroundControllerTestDB(t, 5000)
	recorder, ctx := newPlaygroundChatRequest(t, userId)

	Playground(ctx)

	require.NotEqual(t, http.StatusForbidden, recorder.Code)
	payload := decodePlaygroundErrorPayload(t, recorder)
	require.NotEqual(t, types.ErrorCodeInsufficientUserQuota, payload.Error.Code)
}
