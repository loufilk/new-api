package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerifiedSupportEmailRejectsUnprovenOrMalformedAddresses(t *testing.T) {
	for _, test := range []struct {
		name string
		user User
		want bool
	}{
		{name: "legacy profile", user: User{Email: "owner@example.com"}},
		{name: "address changed", user: User{Email: "new@example.com", EmailVerifiedAddress: "old@example.com", EmailVerificationVersion: 1, EmailVerifiedAt: 1}},
		{name: "header injection", user: User{Email: "owner@example.com\r\nBcc: victim@example.com", EmailVerifiedAddress: "owner@example.com\r\nbcc: victim@example.com", EmailVerificationVersion: 1, EmailVerifiedAt: 1}},
		{name: "multiple recipients", user: User{Email: "owner@example.com,victim@example.com", EmailVerifiedAddress: "owner@example.com,victim@example.com", EmailVerificationVersion: 1, EmailVerifiedAt: 1}},
		{name: "verified", user: User{Email: "Owner@Example.com", EmailVerifiedAddress: "owner@example.com", EmailVerificationVersion: 1, EmailVerifiedAt: 1}, want: true},
	} {
		t.Run(test.name, func(t *testing.T) { assert.Equal(t, test.want, VerifiedSupportEmail(&test.user)) })
	}
	assert.False(t, VerifiedSupportEmail(nil))
}

func TestVerifiedSupportEmailCannotBeAssignedThroughUserJSON(t *testing.T) {
	var user User
	require.NoError(t, common.Unmarshal([]byte(`{"email":"owner@example.com","email_verified_address":"owner@example.com","email_verification_version":1,"email_verified_at":1,"EmailVerifiedAddress":"owner@example.com","EmailVerificationVersion":1,"EmailVerifiedAt":1}`), &user))
	assert.False(t, VerifiedSupportEmail(&user))
	assert.Empty(t, user.EmailVerifiedAddress)
	assert.Zero(t, user.EmailVerificationVersion)
	assert.Zero(t, user.EmailVerifiedAt)
}
