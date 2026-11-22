package moneyport

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

const (
	SourceWallet       = "wallet"
	SourceSubscription = "subscription"
)

// FundingSource is the minimal runtime funding contract consumed by billing
// orchestration. Optional behavior is expressed through the capability
// interfaces in funding.go.
type FundingSource interface {
	Source() string
	PreConsume(amount int) error
	Settle(delta int) error
	Refund() error
}

// FactoryRequest contains only stable construction facts. It deliberately does
// not expose gin.Context, RelayInfo, root model structs, or arbitrary maps.
type FactoryRequest struct {
	UserID       int
	RequestID    string
	BillingModel string
	Amount       int
	AccountOrder []string
}

// Clone returns an independent request so factory implementations cannot mutate
// the caller's account-order slice.
func (r FactoryRequest) Clone() FactoryRequest {
	r.AccountOrder = append([]string(nil), r.AccountOrder...)
	return r
}

type FundingFactory interface {
	Create(FactoryRequest) (FundingSource, error)
}

type FundingFactoryFunc func(FactoryRequest) (FundingSource, error)

func (f FundingFactoryFunc) Create(request FactoryRequest) (FundingSource, error) {
	return f(request)
}

var (
	ErrFactoryRegistryFrozen = errors.New("moneyport: funding factory registry is frozen")
	ErrFactoryNotRegistered  = errors.New("moneyport: funding factory is not registered")
)

// Stable funding errors live in the generic port so billing orchestration can
// map them without importing a concrete wallet implementation.
var (
	ErrWalletInsufficientBalance = errors.New("wallet quota insufficient")
	ErrBonusFundingNotSupported  = errors.New("该模型不支持赠金支付，付费余额不足")
)

// FactoryRegistry is configured at application composition time and frozen
// before request handling. It has no process-global singleton; the host owns the
// active registry reference.
type FactoryRegistry struct {
	mu        sync.RWMutex
	frozen    bool
	factories map[string]FundingFactory
}

func NewFactoryRegistry() *FactoryRegistry {
	return &FactoryRegistry{factories: make(map[string]FundingFactory)}
}

func normalizeSource(source string) string { return strings.ToLower(strings.TrimSpace(source)) }

func (r *FactoryRegistry) Register(source string, factory FundingFactory) error {
	if r == nil {
		return errors.New("moneyport: nil funding factory registry")
	}
	source = normalizeSource(source)
	if source == "" {
		return errors.New("moneyport: funding source is empty")
	}
	if factory == nil {
		return fmt.Errorf("moneyport: funding factory %q is nil", source)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return ErrFactoryRegistryFrozen
	}
	if _, exists := r.factories[source]; exists {
		return fmt.Errorf("moneyport: duplicate funding factory %q", source)
	}
	r.factories[source] = factory
	return nil
}

func (r *FactoryRegistry) Freeze() error {
	if r == nil {
		return errors.New("moneyport: nil funding factory registry")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frozen = true
	return nil
}

func (r *FactoryRegistry) Frozen() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.frozen
}

func (r *FactoryRegistry) Create(source string, request FactoryRequest) (FundingSource, error) {
	if r == nil {
		return nil, errors.New("moneyport: nil funding factory registry")
	}
	source = normalizeSource(source)
	r.mu.RLock()
	factory := r.factories[source]
	frozen := r.frozen
	r.mu.RUnlock()
	if !frozen {
		return nil, errors.New("moneyport: funding factory registry is not frozen")
	}
	if factory == nil {
		return nil, fmt.Errorf("%w: %s", ErrFactoryNotRegistered, source)
	}
	funding, err := factory.Create(request.Clone())
	if err != nil {
		return nil, err
	}
	if funding == nil {
		return nil, fmt.Errorf("moneyport: funding factory %q returned nil", source)
	}
	if normalizeSource(funding.Source()) != source {
		return nil, fmt.Errorf("moneyport: funding factory %q returned source %q", source, funding.Source())
	}
	return funding, nil
}
