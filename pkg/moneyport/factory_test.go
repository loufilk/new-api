package moneyport

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type factoryTestFunding struct{ source string }

func (f *factoryTestFunding) Source() string     { return f.source }
func (*factoryTestFunding) PreConsume(int) error { return nil }
func (*factoryTestFunding) Settle(int) error     { return nil }
func (*factoryTestFunding) Refund() error        { return nil }

func TestFactoryRegistryFreezesAndClonesRequests(t *testing.T) {
	reg := NewFactoryRegistry()
	var got FactoryRequest
	require.NoError(t, reg.Register(SourceWallet, FundingFactoryFunc(func(request FactoryRequest) (FundingSource, error) {
		got = request
		request.AccountOrder[0] = "mutated"
		return &factoryTestFunding{source: SourceWallet}, nil
	})))
	require.NoError(t, reg.Freeze())
	assert.True(t, reg.Frozen())
	assert.ErrorIs(t, reg.Register("other", FundingFactoryFunc(func(FactoryRequest) (FundingSource, error) { return nil, nil })), ErrFactoryRegistryFrozen)

	request := FactoryRequest{UserID: 1, RequestID: "r", BillingModel: "m", Amount: 9, AccountOrder: []string{"bonus", "paid"}}
	funding, err := reg.Create(" WALLET ", request)
	require.NoError(t, err)
	assert.Equal(t, SourceWallet, funding.Source())
	assert.Equal(t, "bonus", request.AccountOrder[0], "factory receives a defensive copy")
	assert.Equal(t, 1, got.UserID)
}

func TestFactoryRegistryRejectsMissingNilAndWrongSourceFactories(t *testing.T) {
	reg := NewFactoryRegistry()
	require.NoError(t, reg.Register(SourceWallet, FundingFactoryFunc(func(FactoryRequest) (FundingSource, error) {
		return &factoryTestFunding{source: SourceSubscription}, nil
	})))
	require.NoError(t, reg.Freeze())
	_, err := reg.Create(SourceWallet, FactoryRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "returned source")

	_, err = reg.Create(SourceSubscription, FactoryRequest{})
	assert.ErrorIs(t, err, ErrFactoryNotRegistered)

	reg2 := NewFactoryRegistry()
	assert.Error(t, reg2.Register("", FundingFactoryFunc(func(FactoryRequest) (FundingSource, error) { return nil, nil })))
	assert.Error(t, reg2.Register("x", nil))

	boom := errors.New("boom")
	reg3 := NewFactoryRegistry()
	require.NoError(t, reg3.Register("x", FundingFactoryFunc(func(FactoryRequest) (FundingSource, error) { return nil, boom })))
	require.NoError(t, reg3.Freeze())
	_, err = reg3.Create("x", FactoryRequest{})
	assert.ErrorIs(t, err, boom)
}
