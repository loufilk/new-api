package service

import (
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type streamFailureFunding struct {
	mu          sync.Mutex
	settleCalls int
	refundCalls int
	outstanding int
	refunded    chan struct{}
}

func (f *streamFailureFunding) Source() string       { return "stream_failure_test" }
func (f *streamFailureFunding) PreConsume(int) error { return nil }
func (f *streamFailureFunding) Settle(int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settleCalls++
	return nil
}
func (f *streamFailureFunding) Refund() error {
	f.mu.Lock()
	f.refundCalls++
	f.outstanding = 0
	f.mu.Unlock()
	if f.refunded != nil {
		select {
		case f.refunded <- struct{}{}:
		default:
		}
	}
	return nil
}
func (f *streamFailureFunding) OutstandingReserve() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.outstanding
}
func (f *streamFailureFunding) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.settleCalls, f.refundCalls
}

func TestStreamFailurePartialUsageSettlesOnceAndNeverRefunds(t *testing.T) {
	funding := &streamFailureFunding{outstanding: 100}
	session := &BillingSession{
		relayInfo:        &relaycommon.RelayInfo{IsPlayground: true},
		funding:          funding,
		preConsumedQuota: 100,
		tokenConsumed:    100,
	}

	require.NoError(t, session.Settle(60))
	require.NoError(t, session.Settle(60), "repeated settlement must be idempotent")
	session.Refund(nil)
	session.Refund(nil)

	settleCalls, refundCalls := funding.counts()
	assert.Equal(t, 1, settleCalls)
	assert.Zero(t, refundCalls, "a settled partial failure must never refund the same reservation")
	assert.False(t, session.NeedsRefund())
}

func TestStreamFailureWithoutUsageRefundsReservationOnce(t *testing.T) {
	if runRefundFixtureChild(t) {
		return
	}
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	funding := &streamFailureFunding{outstanding: 100, refunded: make(chan struct{}, 2)}
	session := &BillingSession{
		relayInfo:        &relaycommon.RelayInfo{IsPlayground: true},
		funding:          funding,
		preConsumedQuota: 100,
		// Unit state corresponding to an official reserved request. Playground
		// deliberately skips actual token SQL; OutstandingReserve is not used.
		tokenConsumed: 100,
	}
	t.Cleanup(func() { waitRefundCallback(t, session) })

	require.True(t, session.NeedsRefund())
	session.Refund(c)
	session.Refund(c)

	select {
	case <-funding.refunded:
	case <-time.After(2 * time.Second):
		t.Fatal("refund did not execute")
	}
	waitRefundCallback(t, session)
	select {
	case <-funding.refunded:
		t.Fatal("refund executed more than once")
	default:
	}

	settleCalls, refundCalls := funding.counts()
	assert.Zero(t, settleCalls)
	assert.Equal(t, 1, refundCalls)
	assert.False(t, session.NeedsRefund())
}
