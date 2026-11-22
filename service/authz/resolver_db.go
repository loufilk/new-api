package authz

import (
	"slices"

	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// CanInTx resolves one permission from the current stored policy, not a
// node-local snapshot. Mutating callers must first lock and revalidate the
// actor row in tx, which also serializes with administrative user edits.
func CanInTx(tx *gorm.DB, userID int, systemRole int, permission Permission) (bool, error) {
	roles := resolveSubjectRoles(userID, systemRole)
	if len(roles) == 0 || !isKnownPermission(permission) {
		return false, nil
	}
	if slices.ContainsFunc(roles, isSuperuserRole) {
		return true, nil
	}
	userSubject := UserSubject(userID)
	subjects := []string{userSubject}
	for _, role := range roles {
		subjects = append(subjects, RoleSubject(role))
	}
	var rules []model.CasbinRule
	if err := tx.Where("ptype = ? AND v0 IN ? AND v1 = ? AND v2 = ?", "p", subjects, permission.Resource, permission.Action).Find(&rules).Error; err != nil {
		return false, err
	}
	effects := make(map[string]string, len(subjects))
	for _, rule := range rules {
		effect, _, err := storedPolicyEffect(rule)
		if err != nil {
			return false, err
		}
		if effects[rule.V0] != EffectDeny {
			effects[rule.V0] = effect
		}
	}
	if effect, exists := effects[userSubject]; exists {
		return effect == EffectAllow, nil
	}
	for _, subject := range subjects[1:] {
		if effects[subject] == EffectAllow {
			return true, nil
		}
	}
	return false, nil
}
