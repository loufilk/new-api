package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestInsertPersistsValidatedInviterRelationship(t *testing.T) {
	setupLedgerTestState(t)
	require.NoError(t, DB.Exec("DELETE FROM users").Error)

	inviter := &User{
		Username: "referral-inviter",
		Password: "placeholder",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		AffCode:  "referral-inviter-code",
	}
	require.NoError(t, DB.Create(inviter).Error)

	invitee := &User{Username: "referral-invitee", Password: "a-valid-password"}
	require.NoError(t, invitee.Insert(inviter.Id))

	var stored User
	require.NoError(t, DB.Select("id, inviter_id").First(&stored, invitee.Id).Error)
	assert.Equal(t, inviter.Id, stored.InviterId)
}

func TestInsertWithTxRejectsDisabledAndSelfInviter(t *testing.T) {
	setupLedgerTestState(t)
	require.NoError(t, DB.Exec("DELETE FROM users").Error)

	disabled := &User{
		Username: "referral-disabled",
		Password: "placeholder",
		Status:   common.UserStatusDisabled,
		Role:     common.RoleCommonUser,
		AffCode:  "referral-disabled-code",
	}
	require.NoError(t, DB.Create(disabled).Error)

	invitee := &User{Username: "referral-oauth-invitee", Password: "a-valid-password"}
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return invitee.InsertWithTx(tx, disabled.Id)
	}))
	invitee.FinalizeOAuthUserCreation(invitee.InviterId)

	var stored User
	require.NoError(t, DB.Select("id, inviter_id").First(&stored, invitee.Id).Error)
	assert.Zero(t, stored.InviterId)
	var disabledStored User
	require.NoError(t, DB.Select("aff_count").First(&disabledStored, disabled.Id).Error)
	assert.Zero(t, disabledStored.AffCount)

	self := &User{Username: "referral-self", Password: "a-valid-password"}
	// A preallocated id models an internal caller accidentally passing its own
	// id. The insert boundary must never create a self-referral.
	self.Id = stored.Id + 100
	require.NoError(t, self.InsertWithTx(DB, self.Id))
	var selfStored User
	require.NoError(t, DB.Select("id, inviter_id").First(&selfStored, self.Id).Error)
	assert.Zero(t, selfStored.InviterId)
}
