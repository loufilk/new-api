package router

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// seedRouteScopedToken stores only a fingerprint and exercises real PAT lookup.
func seedRouteScopedToken(t *testing.T, db *gorm.DB, userID int, raw string, scopes ...string) *model.UserAccessToken {
	t.Helper()
	require.True(t, strings.HasPrefix(raw, model.AccessTokenPrefix))
	token := &model.UserAccessToken{
		UserId: userID, Name: "route-contract", TokenHash: model.AccessTokenFingerprint(raw),
		ExpiresAt: common.GetTimestamp() + 3600,
	}
	require.NoError(t, token.SetScopes(scopes))
	require.NoError(t, db.Create(token).Error)
	return token
}
