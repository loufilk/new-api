package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAccountPasswordRejectsCommonPasswords(t *testing.T) {
	require.GreaterOrEqual(t, len(accountPasswordBlocklist), 3000, "ASVS 5.0.0-6.2.4 requires at least 3000 entries matching the password policy")
	for _, password := range []string{"password", "12345678", "Password123", "aaaaaaaa"} {
		t.Run(password, func(t *testing.T) {
			err := ValidateNewAccountPassword(password)
			require.Error(t, err)
			assert.Equal(t, "This password is too common. Choose a different password.", err.Error())
		})
	}
	require.NoError(t, ValidateNewAccountPassword("Cedar-river-782!quiet"))
}

func TestExistingCommonPasswordRemainsUsableUntilChanged(t *testing.T) {
	hash, err := Password2Hash("password")
	require.NoError(t, err)
	assert.True(t, ValidatePasswordAndHash("password", hash))
	assert.False(t, ValidatePasswordAndHash("Password", hash))
}
