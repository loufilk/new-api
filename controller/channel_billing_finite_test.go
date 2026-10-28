package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateChannelNewAPIBalanceRejectsNonFiniteConversion(t *testing.T) {
	for _, tc := range []struct {
		name         string
		remaining    string
		quotaPerUnit string
		want         float64
		wantErr      bool
	}{
		{name: "positive overflow", remaining: "1e308", quotaPerUnit: "1e-308", wantErr: true},
		{name: "negative overflow", remaining: "-1e308", quotaPerUnit: "1e-308", wantErr: true},
		{name: "zero conversion rate", remaining: "1", quotaPerUnit: "0", wantErr: true},
		{name: "negative conversion rate", remaining: "1", quotaPerUnit: "-1", wantErr: true},
		{name: "tiny finite amounts", remaining: "1e-308", quotaPerUnit: "1e-308", want: 1},
		{name: "large finite balance", remaining: "1e308", quotaPerUnit: "1", want: 1e308},
		{name: "negative finite balance", remaining: "-1250000", quotaPerUnit: "1000000", want: -1.25},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupSub2APIBalanceTest(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/usage/token/":
					_, _ = fmt.Fprintf(w, `{"code":true,"data":{"total_available":%s,"unlimited_quota":false}}`, tc.remaining)
				case "/api/status":
					_, _ = fmt.Fprintf(w, `{"success":true,"data":{"quota_per_unit":%s}}`, tc.quotaPerUnit)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			baseURL := server.URL
			channel := &model.Channel{
				Type: constant.ChannelTypeNewAPI, BaseURL: &baseURL,
				Key: "sk-synthetic", Name: "finite-new-api-balance", Balance: 12.5,
				BalanceUpdatedTime: 123,
			}
			require.NoError(t, model.DB.Create(channel).Error)

			result, err := updateChannelBalance(channel)
			stored := model.Channel{}
			require.NoError(t, model.DB.First(&stored, channel.Id).Error)
			if tc.wantErr {
				assert.Error(t, err, "invalid conversion must fail before updating the channel")
				assert.Equal(t, 12.5, channel.Balance, "in-memory balance must be preserved")
				assert.Equal(t, 12.5, stored.Balance, "persisted balance must be preserved")
				assert.EqualValues(t, 123, stored.BalanceUpdatedTime, "a failed fetch must not appear fresh")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, result.Balance)
			assert.Equal(t, tc.want, channel.Balance)
			assert.Equal(t, tc.want, stored.Balance)
		})
	}
}
