package model

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFingerprintRetentionRequiresDurableSettlement(t *testing.T) {
	_, _, now := fingerprintSiteFixture(t)
	require.NoError(t, DB.AutoMigrate(&ModelTestRun{}, &BillingFinalization{}))
	for _, tc := range []struct {
		name, state, status                                 string
		age                                                 int64
		legacy, noDispatch, malformed, mismatch, anonymized bool
	}{
		{name: "settled", state: BillingSettled, status: "completed", age: 86401, anonymized: true},
		{name: "refunded", state: BillingRefunded, status: "failed", age: 86401, anonymized: true},
		{name: "partial-settlement", state: BillingSettled, status: "completed", age: 86401},
		{name: "missing-receipt", status: "completed", age: 86401},
		{name: "held", state: BillingHeld, status: "failed", age: 86401},
		{name: "unknown-but-settled", state: BillingSettled, status: "unknown", age: 86401},
		{name: "unknown", state: BillingDeliveryUnknown, status: "unknown", age: 86401},
		{name: "refund-review", state: BillingRefundReview, status: "failed", age: 86401},
		{name: "refund-ready", state: BillingRefundReady, status: "failed", age: 86401},
		{name: "settlement-review", state: BillingSettlementReview, status: "completed", age: 86401},
		{name: "running", state: BillingSettled, status: "running", age: 86401},
		{name: "recent", state: BillingSettled, status: "completed", age: 86399},
		{name: "legacy", state: BillingSettled, status: "completed", age: 86401, legacy: true},
		{name: "no-dispatch", status: "cancelled", age: 86401, noDispatch: true, anonymized: true},
		{name: "malformed", state: BillingSettled, status: "completed", age: 86401, malformed: true},
		{name: "wrong-owner", state: BillingSettled, status: "completed", age: 86401, mismatch: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			confirmation := "confirm-" + tc.name
			run := ModelTestRun{ID: tc.name, Source: "user", UserID: 101, TokenID: 102, ConfirmationID: &confirmation, Model: "test", Group: "default", BenchmarkID: "local-fingerprint-v1", Status: tc.status, Assessment: "scored", CreatedAt: now - tc.age, CompletedAt: now - tc.age + 1, ChannelID: 103, RequestID: "outer-" + tc.name, FingerprintResult: JSONValue(`{"status":"scored"}`), SamplesJSON: fmt.Sprintf(`[{"request_id":%q,"status":"completed"}]`, tc.name)}
			if tc.name == "partial-settlement" {
				run.SamplesJSON = fmt.Sprintf(`[{"request_id":%q},{"request_id":"missing-second"}]`, tc.name)
			}
			if tc.legacy {
				run.BenchmarkID = "pelican-static-v1"
			}
			if tc.noDispatch {
				run.SamplesJSON = ""
				run.ChannelID = 0
			}
			if tc.malformed {
				run.SamplesJSON = "bad-json"
			}
			require.NoError(t, DB.Create(&run).Error)
			if tc.state != "" {
				user := run.UserID
				if tc.mismatch {
					user++
				}
				require.NoError(t, DB.Create(&BillingFinalization{RequestID: tc.name, UserID: user, TokenID: run.TokenID, State: tc.state}).Error)
			}
			for range 2 {
				require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return anonymizeExpiredModelTestRuns(tx, now) }))
			}
			var stored ModelTestRun
			require.NoError(t, DB.First(&stored, "id = ?", run.ID).Error)
			assert.Equal(t, run.ConfirmationID, stored.ConfirmationID)
			assert.Equal(t, run.FingerprintResult, stored.FingerprintResult)
			assert.Equal(t, run.Model, stored.Model)
			assert.Equal(t, run.Group, stored.Group)
			assert.Equal(t, run.Status, stored.Status)
			assert.Equal(t, "user", stored.Source)
			if tc.anonymized {
				assert.Zero(t, stored.UserID)
				assert.Zero(t, stored.TokenID)
				assert.Empty(t, stored.SamplesJSON)
				assert.Empty(t, stored.RequestID)
				assert.Zero(t, stored.ChannelID)
			} else {
				assert.Equal(t, run.UserID, stored.UserID)
				assert.Equal(t, run.SamplesJSON, stored.SamplesJSON)
				assert.Equal(t, run.RequestID, stored.RequestID)
			}
		})
	}
}
