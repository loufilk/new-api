package model

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

type AgentInvitationPolicyOperation struct {
	ID          int64  `json:"id" gorm:"primaryKey"`
	RequestID   string `json:"request_id" gorm:"type:varchar(96);not null;uniqueIndex"`
	ActorUserID int    `json:"actor_user_id" gorm:"not null;index"`
	Signature   string `json:"-" gorm:"type:char(64);not null"`
	Result      string `json:"-" gorm:"type:text;not null"`
	CreatedAt   int64  `json:"created_at" gorm:"autoCreateTime"`
}

type AgentInvitationPolicyMutation struct {
	ExpectedRevision int64 `json:"expected_revision"`
	Enabled          bool  `json:"enabled"`
	Quota            int   `json:"quota"`
}

func AgentInvitationPolicyTransactionContext(actorID int, profileVersion, policyVersion int64, requestID string, mutation AgentInvitationPolicyMutation) (AgentTransactionContext, error) {
	if mutation.ExpectedRevision < 0 || mutation.Quota < 0 || mutation.Quota > common.MaxWalletQuota {
		return AgentTransactionContext{}, ErrAgentInvitationPolicy
	}
	return NewAgentTransactionContext("invitation.policy.update", requestID, actorID, profileVersion, policyVersion, "", "0", int64(mutation.Quota), "invitation_policy", "1", mutation.ExpectedRevision, mutation)
}

func requireAgentInvitationPolicyReaderTx(tx *gorm.DB, identity AuthSessionIdentity) error {
	if err := validateAgentProfileOperationReaderTx(tx, identity); err != nil {
		return err
	}
	var actor User
	if err := tx.Select("id", "role").First(&actor, identity.UserID).Error; err != nil {
		return err
	}
	if actor.Role != common.RoleRootUser {
		return ErrAgentInvitationPermission
	}
	return nil
}

func prepareAgentInvitationPolicyOperation(identity AuthSessionIdentity, requestID string, payload []byte) (*AgentPreparedOperation, error) {
	var mutation AgentInvitationPolicyMutation
	var fields map[string]any
	if common.DecodeJsonStrict(bytes.NewReader(payload), &mutation) != nil || common.Unmarshal(payload, &fields) != nil || len(fields) != 3 {
		return nil, ErrAgentInvitationPolicy
	}
	for _, key := range []string{"expected_revision", "enabled", "quota"} {
		if fields[key] == nil {
			return nil, ErrAgentInvitationPolicy
		}
	}
	var result AgentPreparedOperation
	err := DB.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).Transaction(func(tx *gorm.DB) error {
		current, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		policy, err := lockAgentInvitationPolicyTx(tx)
		if err != nil {
			return err
		}
		if policy.Revision != mutation.ExpectedRevision {
			return ErrAgentProfileConflict
		}
		profile, err := readAgentProfile(tx, identity.UserID, false)
		if err != nil {
			return err
		}
		version := int64(0)
		if profile != nil {
			version = profile.Version
		}
		if _, err := RequireAgentCapabilityTx(tx, identity, identity.UserID, version, AgentCapabilityAdmin); err != nil {
			return err
		}
		var actor User
		if err := tx.Select("id", "role").First(&actor, identity.UserID).Error; err != nil {
			return err
		}
		if actor.Role != common.RoleRootUser {
			return ErrAgentInvitationPermission
		}
		result.VerificationContext, err = AgentInvitationPolicyTransactionContext(identity.UserID, version, current.ID, requestID, mutation)
		if err != nil {
			return err
		}
		result.Summary = AgentOperationSummary{Action: "invitation.policy.update", AgentUserID: identity.UserID, AmountMinor: "0", FeeMinor: "0", NetMinor: "0", Quota: int64(mutation.Quota), TargetLabel: fmt.Sprintf("Registration test bonus enabled=%t; quota=%d; previous revision=%d", mutation.Enabled, mutation.Quota, mutation.ExpectedRevision), PolicyVersion: current.ID}
		result.StatusURL = "/api/agent-platform/operations/" + requestID
		return nil
	})
	return &result, err
}

// UpdateAgentInvitationPolicyAuthorized changes only future registration grants.
// Existing attribution snapshots and credited wallet balances are never changed.
func UpdateAgentInvitationPolicyAuthorized(authorization AgentTransactionAuthorization, requestID string, mutation AgentInvitationPolicyMutation) (*AgentInvitationPolicy, error) {
	if validateAgentRequestID(requestID) != nil || mutation.ExpectedRevision < 0 || mutation.Quota < 0 || mutation.Quota > common.MaxWalletQuota {
		return nil, ErrAgentInvitationPolicy
	}
	signature, err := AgentTransactionParametersHash(mutation)
	if err != nil {
		return nil, err
	}
	identity := authorization.Identity()
	var result AgentInvitationPolicy
	err = DB.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).Transaction(func(tx *gorm.DB) error {
		current, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		operation := AgentInvitationPolicyOperation{RequestID: requestID, ActorUserID: identity.UserID, Signature: signature}
		claimed := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "request_id"}}, DoNothing: true}).Create(&operation)
		if claimed.Error != nil {
			return claimed.Error
		}
		if claimed.RowsAffected == 0 {
			if err := lockForUpdate(tx).Where("request_id = ?", requestID).First(&operation).Error; err != nil {
				return err
			}
			if operation.ActorUserID != identity.UserID || operation.Signature != signature {
				return ErrAgentProfileConflict
			}
			if err := requireAgentInvitationPolicyReaderTx(tx, identity); err != nil {
				return err
			}
			if common.UnmarshalJsonStr(operation.Result, &result) != nil {
				return ErrAgentProfileConflict
			}
			return nil
		}
		policy, err := lockAgentInvitationPolicyTx(tx)
		if err != nil {
			return err
		}
		if policy.Revision != mutation.ExpectedRevision {
			return ErrAgentProfileConflict
		}
		// This version read is only a candidate binding. Consume rechecks it
		// under users -> Profile -> session/proof locks before any mutation.
		profile, err := readAgentProfile(tx, identity.UserID, false)
		if err != nil {
			return err
		}
		version := int64(0)
		if profile != nil {
			version = profile.Version
		}
		expected, err := AgentInvitationPolicyTransactionContext(identity.UserID, version, current.ID, requestID, mutation)
		if err != nil {
			return err
		}
		if err := ConsumeAgentTransactionAuthorizationTx(tx, authorization, expected); err != nil {
			return err
		}
		if err := applyAgentInvitationPolicyValuesTx(tx, policy, identity.UserID, mutation.Enabled, mutation.Quota); err != nil {
			return err
		}
		policy.PaymentComplianceConfirmed = operation_setting.IsPaymentComplianceConfirmed()
		result = *policy
		encoded, err := common.Marshal(result)
		if err != nil {
			return err
		}
		return tx.Model(&operation).Update("result", string(encoded)).Error
	})
	return &result, err
}

func findAgentInvitationPolicyOperation(identity AuthSessionIdentity, requestID string) (*AgentOperationStatus, error) {
	var operation AgentInvitationPolicyOperation
	if err := DB.Where("request_id = ? AND actor_user_id = ?", requestID, identity.UserID).First(&operation).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if err := DB.Transaction(func(tx *gorm.DB) error { return requireAgentInvitationPolicyReaderTx(tx, identity) }); err != nil {
		return nil, err
	}
	var policy AgentInvitationPolicy
	if common.UnmarshalJsonStr(operation.Result, &policy) != nil {
		return nil, ErrAgentProfileConflict
	}
	return &AgentOperationStatus{RequestID: requestID, Action: "invitation.policy.update", Status: "completed", ResourceType: "invitation_policy", ResourceID: "1", ResourceState: "published", Resource: policy}, nil
}
