package common

import "testing"

func TestLegacyConsumerRoleNeverGrantsAdministration(t *testing.T) {
	for _, role := range []int{RoleGuestUser, RoleCommonUser, RoleAdminUser, RoleRootUser} {
		if got := EffectiveUserRole(role); got != role {
			t.Fatalf("role %d changed to %d", role, got)
		}
	}
	if got := EffectiveUserRole(RoleAgentUser); got != RoleCommonUser {
		t.Fatalf("historical role must be ordinary, got %d", got)
	}
}
