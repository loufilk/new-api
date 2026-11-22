package wallet

import (
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/walletport"
)

// Policy keeps ChuanAPI's bonus-balance commercial rule outside the generic
// billing engine. Root model access remains the temporary pricing/data adapter.
type Policy struct{}

var _ walletport.Policy = Policy{}

func (Policy) BonusEligible(billingModel string) bool {
	return model.IsModelBonusEligible(billingModel)
}

func (p Policy) AccountOrder(billingModel string) []string {
	if p.BonusEligible(billingModel) {
		return []string{walletport.AccountBonus, walletport.AccountReward, walletport.AccountPaid}
	}
	// 非赠金模型：邀请奖励可全模型消费，顺序 reward -> paid。
	return []string{walletport.AccountReward, walletport.AccountPaid}
}

func RegisterRuntime() error {
	return walletport.SetDefault(Policy{})
}
