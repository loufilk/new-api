package service

import (
	"crypto/subtle"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/pquerna/otp/totp"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// PrepareAgentTransactionAuthorization validates the opaque, operation-bound
// proof without consuming it. Only the model's financial transaction may claim
// it. A controller must never substitute this check for Consume...Tx.
func PrepareAgentTransactionAuthorization(raw string, identity AuthIdentity, context model.AgentTransactionContext) (model.AgentTransactionAuthorization, error) {
	if _, pat := model.ParseAccessTokenSessionID(identity.SessionID); pat {
		return model.AgentTransactionAuthorization{}, ErrVerificationForbidden
	}
	if err := ValidateStepUpIdentity(identity); err != nil {
		return model.AgentTransactionAuthorization{}, err
	}
	return model.PrepareAgentTransactionAuthorization(raw, identity, context)
}

// claimAgentTransactionTOTP makes the accepted time-step single-use across all
// financial requests and servers. No OTP or factor secret is persisted in the
// claim. Include all matching counters to prevent a boundary/collision replay.
func claimAgentTransactionTOTP(twoFA *model.TwoFA, code string) error {
	clean, err := common.ValidateNumericCode(code)
	if err != nil || twoFA == nil {
		return ErrVerificationFailed
	}
	now := time.Now()
	counter := now.Unix() / 30
	var matching []int64
	for _, offset := range []int64{-1, 0, 1} {
		candidate := counter + offset
		expected, err := totp.GenerateCode(twoFA.Secret, time.Unix(candidate*30, 0))
		if err != nil {
			return ErrVerificationFailed
		}
		if subtle.ConstantTimeCompare([]byte(clean), []byte(expected)) == 1 {
			matching = append(matching, candidate)
		}
	}
	if len(matching) == 0 {
		return ErrVerificationFailed
	}
	return model.DB.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).Transaction(func(tx *gorm.DB) error {
		for _, candidate := range matching {
			assertion := fmt.Sprintf("%d:%d:%d", twoFA.UserId, twoFA.Id, candidate)
			if err := model.ClaimExternalAuthAssertionWithTx(tx, "agent_totp", assertion, now.Add(2*time.Minute)); err != nil {
				return ErrVerificationFailed
			}
		}
		return nil
	})
}
