package model

import (
	"errors"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// TransferWalletToAgentSimple moves only the owner's paid wallet quota, at face
// value. The separate wallet source cannot become cash-refund or income quota.
// User/account locks, both ledgers and the recovery result share one transaction.
func TransferWalletToAgentSimple(identity AuthSessionIdentity, request AgentSimpleTransferRequest) (*AgentSimpleAccountView, error) {
	if validateAgentSimpleRequestID(request.RequestID) != nil || validateAgentSimpleAmount(request.Quota) != nil {
		return nil, ErrAgentSimpleInvalid
	}
	var view *AgentSimpleAccountView
	err := DB.Transaction(func(tx *gorm.DB) error {
		// Lock the owner first, matching wallet writes and exit settlements. This
		// serializes independent request IDs against the same paid balance.
		actor, err := agentSimpleActorTx(tx, identity)
		if err != nil || actor.Role >= common.RoleAdminUser {
			return ErrAgentSimplePermission
		}
		if err := requireAgentSimpleHistoryTx(tx, identity, identity.UserID); err != nil {
			return err
		}
		operation, replay, err := claimAgentRedemptionOperation(tx, "simple_wallet_transfer", request.RequestID, RedemptionScope{UserID: actor.Id, Role: actor.Role}, actor.Id, request)
		if err != nil {
			return err
		}
		if replay {
			view, err = loadAgentSimpleAccountOperationTx(tx, operation)
			return err
		}
		if err := requireAgentSimpleSalesTx(tx, identity, actor.Id); err != nil {
			return err
		}
		account, err := lockAgentSimpleAccountTx(tx, actor.Id, false)
		if err != nil {
			return err
		}
		if account.Status != AgentSimpleStatusActive {
			return ErrAgentSimpleUnavailable
		}
		if request.Quota > int64(common.MaxWalletQuota)-simpleAvailableQuota(account) {
			return ErrWalletQuotaLimitExceeded
		}
		_, err = DebitQuotaTx(tx, LedgerEntry{
			UserId: actor.Id, Account: AccountPaid, Delta: int(request.Quota),
			Reason: "agent_wallet_transfer", ReferenceType: "agent_wallet_transfer",
			ReferenceId: request.RequestID, OperatorId: actor.Id,
			IdempotencyKey: "agent_wallet_transfer:" + strconv.Itoa(actor.Id) + ":" + request.RequestID,
		}, AccountPaid)
		if errors.Is(err, ErrLedgerInsufficientBalance) {
			return ErrAgentSimpleInsufficient
		}
		if err != nil {
			return err
		}
		before := *account
		account.WalletQuota += request.Quota
		if err := saveAgentSimpleAccountTx(tx, before, account, actor.Id, "wallet_transfer", "wallet", request.RequestID, "Transfer paid wallet quota to agent balance at face value", request.RequestID); err != nil {
			return err
		}
		view = agentSimpleAccountView(account, false)
		encoded, err := common.Marshal(view)
		if err != nil {
			return err
		}
		return tx.Model(operation).Update("result", string(encoded)).Error
	})
	if err == nil {
		RefreshUserQuotaCache(identity.UserID, 0)
	}
	return view, err
}
