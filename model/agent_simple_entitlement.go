package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// activeAgentSalesTx keeps invitation benefits and sales permissions consistent.
// Archived profiles alone do not restore sales eligibility after the non-agent edition.
func activeAgentSalesTx(tx *gorm.DB, userID int) (bool, error) {
	profile, err := readAgentProfile(tx, userID, false)
	if err != nil || !profile.Allows(AgentCapabilitySales) {
		return false, err
	}
	var account AgentSimpleAccount
	err = tx.Select("status").Where("agent_user_id = ?", userID).First(&account).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return err == nil && account.Status == AgentSimpleStatusActive, err
}

func requireAgentSimpleHistoryTx(tx *gorm.DB, identity AuthSessionIdentity, agentID int) error {
	actor, err := agentSimpleActorTx(tx, identity)
	if err != nil || actor.Role == common.RoleAdminUser {
		return ErrAgentSimplePermission
	}
	if actor.Role == common.RoleRootUser {
		return nil
	}
	profile, err := readAgentProfile(tx, agentID, false)
	if err != nil || actor.Id != agentID || !profile.Allows(AgentCapabilityHistory) {
		return ErrAgentSimplePermission
	}
	return nil
}

// GetAgentSimpleCapabilities applies the live A07 account boundary to the
// profile-derived display capabilities. Reading capabilities never opens an
// account or reactivates archived qualification.
func GetAgentSimpleCapabilities(user *User, profile *AgentProfile) (AgentCapabilities, error) {
	capabilities := AgentCapabilitiesFor(user, profile)
	if !capabilities.Sales {
		return capabilities, nil
	}
	var account AgentSimpleAccount
	err := DB.Select("status").Where("agent_user_id = ?", user.Id).First(&account).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		capabilities.Sales = false
		return capabilities, nil
	}
	if err != nil {
		return AgentCapabilities{}, err
	}
	capabilities.Sales = account.Status == AgentSimpleStatusActive
	return capabilities, nil
}
