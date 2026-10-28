package walletport

import (
	"errors"
	"sync"
)

const (
	AccountPaid   = "paid"
	AccountBonus  = "bonus"
	AccountReward = "reward"
)

// Policy owns wallet-specific commercial decisions that should not live in the
// generic billing engine. Core billing consumes only the billing-model identity
// and generic account names through this port.
type Policy interface {
	AccountOrder(billingModel string) []string
	BonusEligible(billingModel string) bool
}

var (
	mu               sync.RWMutex
	defaultPolicy    Policy
	ErrNotConfigured = errors.New("walletport: policy is not configured")
)

// SetDefault is composition-time only and write-once.
func SetDefault(policy Policy) error {
	if policy == nil {
		return errors.New("walletport: policy must not be nil")
	}
	mu.Lock()
	defer mu.Unlock()
	if defaultPolicy != nil {
		return errors.New("walletport: policy already configured")
	}
	defaultPolicy = policy
	return nil
}

func Default() (Policy, error) {
	mu.RLock()
	defer mu.RUnlock()
	if defaultPolicy == nil {
		return nil, ErrNotConfigured
	}
	return defaultPolicy, nil
}
