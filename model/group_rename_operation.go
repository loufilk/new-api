package model

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var ErrGroupRenameRequestConflict = errors.New("group rename request ID was already used for another operation")
var ErrGroupRenamePermission = errors.New("group rename requires a current root identity")

// Recheck session/token revocation and privilege after obtaining the namespace
// lock. A request queued behind another rename must not commit after demotion.
func authorizeGroupRenameTx(tx *gorm.DB, identity *AuthSessionIdentity) error {
	if identity == nil {
		return nil
	} // Internal model writers have no HTTP identity.
	if err := ValidateAuthSessionWithTx(tx, *identity); err != nil {
		return ErrGroupRenamePermission
	}
	var actor User
	if err := tx.Select("id", "role").First(&actor, identity.UserID).Error; err != nil {
		return err
	}
	if actor.Role != common.RoleRootUser {
		return ErrGroupRenamePermission
	}
	if tokenID, ok := ParseAccessTokenSessionID(identity.SessionID); ok {
		var token UserAccessToken
		if err := tx.First(&token, tokenID).Error; err != nil {
			return err
		}
		if !slices.Contains(token.GetScopes(), "option:write") {
			return ErrGroupRenamePermission
		}
	}
	return nil
}

// GroupRenameOperation is an immutable, non-secret receipt in the primary DB.
// It commits with the references, so a lost HTTP response or unavailable log DB
// cannot erase who renamed what or cause a retry to execute a second migration.
type GroupRenameOperation struct {
	RequestID   string `gorm:"primaryKey;type:varchar(64)"`
	ActorID     int
	ActorRole   int
	CreatedAt   int64  `gorm:"type:bigint;index"`
	RequestHash string `gorm:"type:varchar(64)"`
	Result      string `gorm:"type:text"`
}

func (*GroupRenameOperation) BeforeUpdate(*gorm.DB) error { return ErrLedgerImmutable }
func (*GroupRenameOperation) BeforeDelete(*gorm.DB) error { return ErrLedgerImmutable }

func newGroupRenameOperation(request GroupRenameApplyRequest) (*GroupRenameOperation, error) {
	if len(request.Mappings) == 0 {
		if request.RequestID != "" {
			return nil, ErrGroupRenameInvalidOption
		}
		return nil, nil
	}
	id := request.RequestID
	if id == "" {
		id = common.GetUUID()
	}
	if len(id) < 16 || len(id) > 64 {
		return nil, ErrGroupRenameInvalidOption
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return nil, ErrGroupRenameInvalidOption
		}
	}
	data, err := common.Marshal(struct {
		Mappings []GroupRenameMapping
		Options  map[string]string
		Expected map[string]string
	}{request.Mappings, request.Options, request.Expected})
	if err != nil {
		return nil, err
	}
	return &GroupRenameOperation{RequestID: id, ActorID: request.ActorID, ActorRole: request.ActorRole,
		CreatedAt: common.GetTimestamp(), RequestHash: fmt.Sprintf("%x", sha256.Sum256(data))}, nil
}

func loadGroupRenameOperationTx(tx *gorm.DB, requested *GroupRenameOperation, result *GroupRenameResult) (bool, error) {
	var stored GroupRenameOperation
	query := tx.Where("request_id = ?", requested.RequestID).Limit(1).Find(&stored)
	if query.Error != nil || query.RowsAffected == 0 {
		return false, query.Error
	}
	if stored.ActorID != requested.ActorID || stored.RequestHash != requested.RequestHash {
		return false, ErrGroupRenameRequestConflict
	}
	if err := common.UnmarshalJsonStr(stored.Result, result); err != nil {
		return false, err
	}
	result.Replayed = true
	return true, nil
}

func saveGroupRenameOperationTx(tx *gorm.DB, operation *GroupRenameOperation, result GroupRenameResult) error {
	data, err := common.Marshal(result)
	if err != nil {
		return err
	}
	operation.Result = string(data)
	return tx.Create(operation).Error
}
