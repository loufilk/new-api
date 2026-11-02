package common

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestConsumeVerificationCodeOnlyOneConcurrentAttemptCanSucceed(t *testing.T) {
	key := "consume-registration@example.invalid"
	RegisterVerificationCodeWithKey(key, "correct-proof", EmailVerificationPurpose)
	t.Cleanup(func() { DeleteKey(key, EmailVerificationPurpose) })
	assert.False(t, ConsumeVerificationCodeWithKey(key, "incorrect", EmailVerificationPurpose))
	var successes atomic.Int32
	var attempts sync.WaitGroup
	for range 2 {
		attempts.Go(func() {
			if ConsumeVerificationCodeWithKey(key, "correct-proof", EmailVerificationPurpose) {
				successes.Add(1)
			}
		})
	}
	attempts.Wait()
	assert.EqualValues(t, 1, successes.Load())
	assert.False(t, VerifyCodeWithKey(key, "correct-proof", EmailVerificationPurpose))
}

func TestConsumeVerificationCodeRejectsExpiredAndWrongPurpose(t *testing.T) {
	key := "expired-registration@example.invalid"
	RegisterVerificationCodeWithKey(key, "correct-proof", EmailVerificationPurpose)
	t.Cleanup(func() { DeleteKey(key, EmailVerificationPurpose) })
	assert.False(t, ConsumeVerificationCodeWithKey(key, "correct-proof", PasswordResetPurpose))
	verificationMutex.Lock()
	verificationMap[EmailVerificationPurpose+key] = verificationValue{code: "correct-proof", time: time.Now().Add(-time.Duration(VerificationValidMinutes)*time.Minute - time.Second)}
	verificationMutex.Unlock()
	assert.False(t, ConsumeVerificationCodeWithKey(key, "correct-proof", EmailVerificationPurpose))
}
