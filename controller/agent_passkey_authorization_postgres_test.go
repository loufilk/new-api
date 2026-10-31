package controller

import (
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Financial integration regression: real software-authenticator
// signatures and PostgreSQL, not an assumed successful Passkey ceremony.
func TestAgentFinancialPasskeyAuthorizationPostgres(t *testing.T) {
	if os.Getenv("TEST_POSTGRES_DSN") == "" {
		t.Skip("requires isolated PostgreSQL financial acceptance; acceptance runner forbids skips")
	}
	t.Setenv("TEST_SECURITY_DIALECT", "postgres")
	for _, verified := range []bool{false, true} {
		name := "reject presence without user verification"
		if verified {
			name = "verified assertion binds original operation and browser"
		}
		t.Run(name, func(t *testing.T) {
			user, identity := setupSecurityEnrollmentTest(t)
			require.NoError(t, model.DB.AutoMigrate(&model.AgentProfile{}, &model.AgentPolicy{}, &model.AgentPolicyState{}))
			key := newSecurityLoginPasskey(t, user.Id)
			context, err := model.NewAgentTransactionContext("commission.withdraw.request", "review-passkey-request", user.Id, 0, 0, "USD", "20", 0, "withdrawal", "review-draft", 1, "immutable financial details")
			require.NoError(t, err)
			begin := decodePasskeyDomainBegin(t, passkeyDomainRequest(t, "/api/user/passkey/verify/begin", map[string]any{"scope": model.AgentTransactionScope, "context": context}, identity, "https://example.com", PasskeyVerifyBegin))
			assertion := passkeyDomainAssertion(t, key, begin.Options.PublicKey.Challenge, "example.com", "https://example.com", user.Id, verified)
			changed := context
			changed.AmountMinor = "2000"
			finish := map[string]any{"flow_token": begin.FlowToken, "credential": assertion, "scope": "password.change", "context": changed}
			response := passkeyDomainRequest(t, "/api/user/passkey/verify/finish", finish, identity, "https://example.com", PasskeyVerifyFinish)
			var result securityEnrollmentResponse
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			if !verified {
				assert.False(t, result.Success, response.Body.String())
				var issued int64
				require.NoError(t, model.DB.Model(&model.AuthFlow{}).Where("purpose = ?", "agent_transaction").Count(&issued).Error)
				assert.Zero(t, issued, "presence-only assertion must not issue financial proof")
				return
			}
			require.True(t, result.Success, response.Body.String())
			var proof service.SecurityProof
			require.NoError(t, common.Unmarshal(result.Data, &proof))
			assert.Equal(t, model.AgentTransactionScope, proof.Scope)
			assert.Equal(t, "passkey", proof.Method)
			assert.Len(t, proof.ProofToken, 43)
			authorization, err := service.PrepareAgentTransactionAuthorization(proof.ProofToken, identity, changed)
			require.NoError(t, err)
			require.Error(t, model.DB.Transaction(func(tx *gorm.DB) error {
				return model.ConsumeAgentTransactionAuthorizationTx(tx, authorization, changed)
			}))
			// A valid second browser session cannot redeem a stolen proof token.
			bundle, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "independent-second-browser")
			require.NoError(t, err)
			other, err := service.ParseAccessToken(bundle.AccessToken)
			require.NoError(t, err)
			authorization, err = service.PrepareAgentTransactionAuthorization(proof.ProofToken, other, context)
			require.NoError(t, err)
			require.Error(t, model.DB.Transaction(func(tx *gorm.DB) error {
				return model.ConsumeAgentTransactionAuthorizationTx(tx, authorization, context)
			}))
			authorization, err = service.PrepareAgentTransactionAuthorization(proof.ProofToken, identity, context)
			require.NoError(t, err)
			require.NoError(t, model.DB.Transaction(func(tx *gorm.DB) error {
				return model.ConsumeAgentTransactionAuthorizationTx(tx, authorization, context)
			}))
			require.ErrorIs(t, model.DB.Transaction(func(tx *gorm.DB) error {
				return model.ConsumeAgentTransactionAuthorizationTx(tx, authorization, context)
			}), model.ErrAuthFlowConsumed)
			response = passkeyDomainRequest(t, "/api/user/passkey/verify/finish", finish, identity, "https://example.com", PasskeyVerifyFinish)
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			assert.False(t, result.Success, "same signed challenge cannot mint another proof")
		})
	}
}
