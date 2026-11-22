package model

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

type AgentProfileOperation struct {
	ID          int64  `json:"id" gorm:"primaryKey"`
	RequestID   string `json:"request_id" gorm:"type:varchar(96);not null;uniqueIndex"`
	ActorUserID int    `json:"actor_user_id" gorm:"not null;index"`
	UserID      int    `json:"user_id" gorm:"not null;index"`
	Signature   string `json:"-" gorm:"type:char(64);not null"`
	Result      string `json:"-" gorm:"type:text;not null"`
	CreatedAt   int64  `json:"created_at" gorm:"autoCreateTime"`
}

type AgentProfileCommand struct {
	UserID int `json:"user_id"`
	AgentProfileMutation
}

func agentProfileOperationContext(command AgentProfileCommand, requestID string, policyVersion int64) (AgentTransactionContext, error) {
	return NewAgentTransactionContext("profile.update", requestID, command.UserID, command.ExpectedVersion, policyVersion, "", "0", 0, "agent_profile", strconv.Itoa(command.UserID), command.ExpectedVersion, command)
}

func PrepareAgentProfileOperation(identity AuthSessionIdentity, action, requestID string, payload []byte) (*AgentPreparedOperation, error) {
	if action == "invitation.policy.update" {
		return prepareAgentInvitationPolicyOperation(identity, requestID, payload)
	}
	if action != "profile.update" {
		return nil, ErrAgentTransactionContext
	}
	var command AgentProfileCommand
	var fields map[string]any
	if common.DecodeJsonStrict(bytes.NewReader(payload), &command) != nil || common.Unmarshal(payload, &fields) != nil || len(fields) != 6 {
		return nil, ErrAgentProfileInvalid
	}
	for _, key := range []string{"user_id", "expected_version", "status", "sales_frozen", "settlement_frozen", "reason"} {
		if fields[key] == nil {
			return nil, ErrAgentProfileInvalid
		}
	}
	var prepared AgentPreparedOperation
	err := DB.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).Transaction(func(tx *gorm.DB) error {
		policy, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		if _, err := RequireAgentCapabilityTx(tx, identity, command.UserID, command.ExpectedVersion, AgentCapabilityAdmin); err != nil {
			return err
		}
		var actor, user User
		if err := tx.First(&actor, identity.UserID).Error; err != nil {
			return err
		}
		if err := tx.First(&user, command.UserID).Error; err != nil {
			return err
		}
		if actor.Id == user.Id || user.Role >= actor.Role {
			return ErrAgentProfilePermission
		}
		if err := validateAgentProfileMutation(command.UserID, command.AgentProfileMutation); err != nil {
			return err
		}
		prepared.VerificationContext, err = agentProfileOperationContext(command, requestID, policy.ID)
		if err != nil {
			return err
		}
		prepared.Summary = AgentOperationSummary{Action: action, AgentUserID: command.UserID, AmountMinor: "0", FeeMinor: "0", NetMinor: "0", TargetLabel: fmt.Sprintf("%s: %s; sales frozen=%t; settlement frozen=%t; %s", user.Username, command.Status, command.SalesFrozen, command.SettlementFrozen, command.Reason), PolicyVersion: policy.ID}
		prepared.StatusURL = "/api/agent-platform/operations/" + requestID
		return nil
	})
	return &prepared, err
}

func UpdateAgentProfileAuthorized(authorization AgentTransactionAuthorization, requestID string, command AgentProfileCommand) (*AgentProfile, error) {
	if validateAgentRequestID(requestID) != nil || validateAgentProfileMutation(command.UserID, command.AgentProfileMutation) != nil {
		return nil, ErrAgentProfileInvalid
	}
	identity := authorization.Identity()
	if identity.UserID <= 0 {
		return nil, ErrAgentTransactionProof
	}
	signature, err := AgentTransactionParametersHash(command)
	if err != nil {
		return nil, err
	}
	var profile AgentProfile
	err = DB.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).Transaction(func(tx *gorm.DB) error {
		policy, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		operation := AgentProfileOperation{RequestID: requestID, ActorUserID: identity.UserID, UserID: command.UserID, Signature: signature}
		claimed := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "request_id"}}, DoNothing: true}).Create(&operation)
		if claimed.Error != nil {
			return claimed.Error
		}
		if claimed.RowsAffected == 0 {
			if err := lockForUpdate(tx).Where("request_id = ?", requestID).First(&operation).Error; err != nil {
				return err
			}
			if operation.ActorUserID != identity.UserID || operation.UserID != command.UserID || operation.Signature != signature {
				return ErrAgentProfileConflict
			}
			if err := validateAgentProfileOperationReaderTx(tx, identity); err != nil {
				return err
			}
			if common.UnmarshalJsonStr(operation.Result, &profile) != nil {
				return ErrAgentProfileConflict
			}
			return nil
		}
		expected, err := agentProfileOperationContext(command, requestID, policy.ID)
		if err != nil {
			return err
		}
		if err := ConsumeAgentTransactionAuthorizationTx(tx, authorization, expected); err != nil {
			return err
		}
		changed, err := UpdateAgentProfileTx(tx, identity, command.UserID, command.AgentProfileMutation)
		if err != nil {
			return err
		}
		profile = *changed
		encoded, err := common.Marshal(profile)
		if err != nil {
			return err
		}
		return tx.Model(&operation).Update("result", string(encoded)).Error
	})
	return &profile, err
}

func validateAgentProfileOperationReaderTx(tx *gorm.DB, identity AuthSessionIdentity) error {
	if _, pat := ParseAccessTokenSessionID(identity.SessionID); pat {
		return ErrAgentProfilePermission
	}
	var actor User
	if err := lockForUpdate(tx).First(&actor, identity.UserID).Error; err != nil {
		return err
	}
	if actor.Status != common.UserStatusEnabled || (actor.Role != common.RoleAdminUser && actor.Role != common.RoleRootUser) {
		return ErrAgentProfilePermission
	}
	return ValidateAuthSessionWithTx(tx, identity)
}

func FindAgentProfileOperation(identity AuthSessionIdentity, requestID string) (*AgentOperationStatus, error) {
	if validateAgentRequestID(requestID) != nil {
		return nil, ErrAgentProfileInvalid
	}
	var operation AgentProfileOperation
	if err := DB.Where("request_id = ? AND actor_user_id = ?", requestID, identity.UserID).First(&operation).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return findAgentInvitationPolicyOperation(identity, requestID)
		}
		return nil, err
	}
	if err := DB.Transaction(func(tx *gorm.DB) error { return validateAgentProfileOperationReaderTx(tx, identity) }); err != nil {
		return nil, err
	}
	var profile AgentProfile
	if common.UnmarshalJsonStr(operation.Result, &profile) != nil {
		return nil, ErrAgentProfileConflict
	}
	return &AgentOperationStatus{RequestID: requestID, Action: "profile.update", Status: "completed", ResourceType: "agent_profile", ResourceID: strconv.Itoa(operation.UserID), ResourceState: profile.Status, Resource: profile}, nil
}
