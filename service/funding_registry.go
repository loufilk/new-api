package service

import (
	"errors"
	"sync"

	"github.com/QuantumNous/new-api/pkg/moneyport"
)

var (
	fundingRegistryMu sync.RWMutex
	fundingRegistry   *moneyport.FactoryRegistry
)

// RegisterHostFundingFactories registers host-owned funding implementations.
// ChuanAPI wallet funding is registered separately by the composition root.
func RegisterHostFundingFactories(reg *moneyport.FactoryRegistry) error {
	if reg == nil {
		return errors.New("service: nil funding factory registry")
	}
	return reg.Register(moneyport.SourceSubscription, moneyport.FundingFactoryFunc(func(request moneyport.FactoryRequest) (moneyport.FundingSource, error) {
		return &SubscriptionFunding{
			requestId: request.RequestID,
			userId:    request.UserID,
			modelName: request.BillingModel,
			amount:    int64(request.Amount),
		}, nil
	}))
}

// ConfigureFundingFactoryRegistry freezes and installs the process registry.
// It is write-once in production; tests configure it once from TestMain.
func ConfigureFundingFactoryRegistry(reg *moneyport.FactoryRegistry) error {
	if reg == nil {
		return errors.New("service: nil funding factory registry")
	}
	if !reg.Frozen() {
		if err := reg.Freeze(); err != nil {
			return err
		}
	}
	fundingRegistryMu.Lock()
	defer fundingRegistryMu.Unlock()
	if fundingRegistry != nil {
		return errors.New("service: funding factory registry already configured")
	}
	fundingRegistry = reg
	return nil
}

func createFundingSource(source string, request moneyport.FactoryRequest) (moneyport.FundingSource, error) {
	fundingRegistryMu.RLock()
	reg := fundingRegistry
	fundingRegistryMu.RUnlock()
	if reg == nil {
		return nil, errors.New("service: funding factory registry is not configured")
	}
	return reg.Create(source, request)
}
