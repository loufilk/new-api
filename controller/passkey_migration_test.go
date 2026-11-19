package controller

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	passkeysvc "github.com/QuantumNous/new-api/service/passkey"
	"github.com/stretchr/testify/require"
)

// v0.16.5 called the statement format "attestation_type" in the SQL mapper.
// Preserve even opaque formats for older gateways; never infer an attestation
// trust type (e.g. Basic or Self) from that column.
func TestPasskeyLegacyCredentialColumns(t *testing.T) {
	for _, format := range []string{"", "none", "packed", "tpm", "android-key", "android-safetynet", "fido-u2f", "apple", "vendor-opaque"} {
		t.Run("format="+format, func(t *testing.T) {
			row := model.PasskeyCredential{
				UserID: 7, CredentialID: "YQ==", PublicKey: "Yg==", AAGUID: "Yw==", AttestationType: format,
				SignCount: 123, CloneWarning: true, UserPresent: true, UserVerified: true,
				BackupEligible: true, BackupState: true, Transports: `["usb","nfc"]`, Attachment: "platform",
			}
			credential := row.ToWebAuthnCredential()
			require.Equal(t, format, credential.AttestationFormat)
			require.Equal(t, format, credential.Descriptor().AttestationFormat)
			require.Empty(t, credential.AttestationType)
			credential.AttestationType = "Self"
			require.Equal(t, &row, model.NewPasskeyCredentialFromWebAuthn(row.UserID, &credential))
		})
	}
}

// These wire templates come from v0.16.5 BeginDiscoverableLogin and
// BeginRegistration. Keep them independent of the current library's marshaler:
// in a rolling upgrade this JSON has already been written by the old gateway.
func TestPasskeyFinishLegacySession(t *testing.T) {
	const challenge = "N7oZbQE5J71_oQdVFomwPwiDdVQfdWApwTArdqkHQA0"
	const origin = "https://example.com"
	const oldLogin = `{"challenge":"%s","rpId":"example.com","user_id":null,"expires":"%s","userVerification":"required"}`
	const oldRegistration = `{"challenge":"%s","rpId":"example.com","user_id":"%s","expires":"%s","userVerification":"required","credParams":[{"type":"public-key","alg":-7},{"type":"public-key","alg":-35},{"type":"public-key","alg":-36},{"type":"public-key","alg":-257},{"type":"public-key","alg":-258},{"type":"public-key","alg":-259},{"type":"public-key","alg":-37},{"type":"public-key","alg":-38},{"type":"public-key","alg":-39},{"type":"public-key","alg":-8}]}`

	for _, purpose := range []string{model.AuthFlowPurposePasskeyLogin, model.AuthFlowPurposePasskeyRegister} {
		t.Run(purpose, func(t *testing.T) {
			user, identity := setupSecurityEnrollmentTest(t)
			expiry := time.Now().Add(2 * time.Minute)
			stamp := expiry.Format(time.RFC3339Nano)
			input := model.AuthFlowCreate{Purpose: purpose, ExpiresAt: expiry}
			var security passkeysvc.FlowSecurity
			var candidateResponse any
			var existing *model.PasskeyCredential
			var err error
			session := fmt.Sprintf(oldLogin, challenge, stamp)
			path, finish := "/api/user/passkey/login/finish", PasskeyLoginFinish
			if purpose == model.AuthFlowPurposePasskeyRegister {
				op := service.VerificationOperation{Scope: service.VerificationScopePasskeyRegister}
				proof := issueSecurityEnrollmentProof(t, identity, op, "password")
				authz, err := service.ConsumeOperationProof(proof, identity, op)
				require.NoError(t, err)
				security = passkeysvc.FlowSecurity{AuthSessionIdentity: identity, Scope: authz.Scope, ContextHash: authz.ContextHash, Authorization: authz}
				input.UserId, input.SessionId = user.Id, identity.SessionID
				handle := base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(user.Id)))
				session = fmt.Sprintf(oldRegistration, challenge, handle, stamp)
				key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
				require.NoError(t, err)
				candidateResponse = securityPasskeyResponse(t, key, challenge, true, 0)
				path, finish = "/api/user/passkey/register/finish", PasskeyRegisterFinish
			} else {
				key := newSecurityLoginPasskey(t, user.Id)
				existing, err = model.GetPasskeyByUserID(user.Id)
				require.NoError(t, err)
				require.NoError(t, model.DB.Model(existing).Update("attestation_type", "packed").Error)
				candidateResponse = passkeyDomainAssertion(t, key, challenge, "example.com", origin, user.Id, true)
			}
			payload, err := common.Marshal(map[string]any{"session_data": json.RawMessage(session), "security": security})
			require.NoError(t, err)
			input.Payload = string(payload)
			token, _, err := model.CreateAuthFlow(input)
			require.NoError(t, err)
			response := passkeyDomainRequest(t, path, map[string]any{"flow_token": token, "credential": candidateResponse}, identity, origin, finish)
			var result securityEnrollmentResponse
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			require.True(t, result.Success, "%s", result.Message)
			row, err := model.GetPasskeyByUserID(user.Id)
			require.NoError(t, err)
			require.NotNil(t, row.RPID)
			require.Equal(t, "example.com", *row.RPID)
			require.True(t, row.UserVerified)
			if existing == nil {
				require.Equal(t, "none", row.AttestationType)
			} else {
				require.Equal(t, "packed", row.AttestationType)
				require.Equal(t, existing.CredentialID, row.CredentialID)
				require.Equal(t, existing.PublicKey, row.PublicKey)
				require.EqualValues(t, 1, row.SignCount)
			}
		})
	}
}
