package model

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const AgentTransactionScope = "agent.transaction"
const agentTransactionFlowPurpose = "agent_transaction"
const AgentTransactionProofTTL = time.Minute

var (
	ErrAgentTransactionContext = errors.New("Invalid financial transaction details")
	ErrAgentTransactionProof   = errors.New("Financial authorization is invalid or no longer matches this transaction")
	ErrAgentTransactionMethod  = errors.New("Financial transactions require an authenticator code or a verified Passkey")
)

// AgentTransactionContext is generated from a server-owned draft/quote. All
// fields are explicit: a missing JSON field must not silently become zero.
// AmountMinor is decimal text so clients cannot lose monetary integer precision.
type AgentTransactionContext struct {
	Action           string `json:"action"`
	RequestID        string `json:"request_id"`
	AgentUserID      int    `json:"agent_user_id"`
	ProfileVersion   int64  `json:"profile_version"`
	PolicyVersion    int64  `json:"policy_version"`
	Currency         string `json:"currency"`
	AmountMinor      string `json:"amount_minor"`
	Quota            int64  `json:"quota"`
	TargetType       string `json:"target_type"`
	TargetID         string `json:"target_id"`
	TargetVersion    int64  `json:"target_version"`
	RecipientVersion int64  `json:"recipient_version"`
	RecipientDigest  string `json:"recipient_digest"`
	ParametersHash   string `json:"parameters_hash"`
}

type agentTransactionRule struct {
	capability      string
	administrator   bool
	root            bool
	optionalProfile bool
}

func agentTransactionActionRule(action string) (agentTransactionRule, bool) {
	switch action {
	case "purchase.legacy.record", "purchase.legacy.confirm":
		return agentTransactionRule{capability: AgentCapabilityAdmin, administrator: true}, true
	case "purchase.payment.record", "purchase.payment.confirm":
		return agentTransactionRule{capability: AgentCapabilitySales, administrator: true}, true
	case "procurement.self_use", "delivery.create", "redemption.create", "redemption.update":
		return agentTransactionRule{capability: AgentCapabilitySales}, true
	case "delivery.cancel", "redemption.cancel", "redemption.delete":
		return agentTransactionRule{capability: AgentCapabilitySettlement}, true
	case "purchase.refund.request":
		return agentTransactionRule{capability: AgentCapabilitySettlement}, true
	case "purchase.refund.approve", "purchase.refund.submit":
		return agentTransactionRule{capability: AgentCapabilitySettlement, administrator: true}, true
	case "purchase.refund.reject":
		return agentTransactionRule{capability: AgentCapabilityAdmin, administrator: true}, true
	case "purchase.refund.resolve", "commission.withdraw.resolve", "commission.withdraw.reject":
		return agentTransactionRule{capability: AgentCapabilityAdmin, administrator: true, optionalProfile: true}, true
	case "commission.withdraw.request", "commission.convert.wallet", "recipient.change":
		return agentTransactionRule{capability: AgentCapabilitySettlement, optionalProfile: true}, true
	case "commission.convert.legacy":
		return agentTransactionRule{capability: AgentCapabilityHistory, optionalProfile: true}, true
	case "commission.convert.procurement":
		return agentTransactionRule{capability: AgentCapabilitySettlement}, true
	case "commission.withdraw.approve", "commission.withdraw.submit":
		return agentTransactionRule{capability: AgentCapabilitySettlement, administrator: true, optionalProfile: true}, true
	case "commission.refund.record", "commission.reserve.release", "commission.outbox.retry":
		return agentTransactionRule{capability: AgentCapabilityAdmin, administrator: true, root: true, optionalProfile: true}, true
	case "profile.update", "redemption.allocate", "redemption.admin.create", "redemption.admin.update", "redemption.admin.delete":
		return agentTransactionRule{capability: AgentCapabilityAdmin, administrator: true, optionalProfile: true}, true
	case "policy.publish", "invitation.policy.update":
		return agentTransactionRule{capability: AgentCapabilityAdmin, administrator: true, root: true, optionalProfile: true}, true
	default:
		return agentTransactionRule{}, false
	}
}

func ValidateAgentTransactionContext(context AgentTransactionContext) error {
	if _, ok := agentTransactionActionRule(context.Action); !ok {
		return ErrAgentTransactionContext
	}
	validRequestID := validateAgentRequestID(context.RequestID) == nil
	if context.Action == "commission.convert.legacy" {
		validRequestID = ValidReferralTransferIdempotencyKey(context.RequestID)
	}
	if !validRequestID || context.AgentUserID <= 0 || context.ProfileVersion < 0 || context.PolicyVersion < 0 || context.TargetVersion < 0 || context.RecipientVersion < 0 || context.Quota < 0 || context.Quota > int64(common.MaxWalletQuota) {
		return ErrAgentTransactionContext
	}
	if len(context.AmountMinor) == 0 || len(context.AmountMinor) > 19 {
		return ErrAgentTransactionContext
	}
	amount, err := strconv.ParseInt(context.AmountMinor, 10, 64)
	if err != nil || amount < 0 || strconv.FormatInt(amount, 10) != context.AmountMinor {
		return ErrAgentTransactionContext
	}
	if context.Currency != "" && (len(context.Currency) != 3 || context.Currency[0] < 'A' || context.Currency[0] > 'Z' || context.Currency[1] < 'A' || context.Currency[1] > 'Z' || context.Currency[2] < 'A' || context.Currency[2] > 'Z') {
		return ErrAgentTransactionContext
	}
	if amount > 0 && context.Currency == "" {
		return ErrAgentTransactionContext
	}
	if context.TargetType == "" || len(context.TargetType) > 64 || context.TargetID == "" || len(context.TargetID) > 128 || strings.TrimSpace(context.TargetID) != context.TargetID || strings.TrimSpace(context.TargetType) != context.TargetType {
		return ErrAgentTransactionContext
	}
	for _, digest := range []string{context.RecipientDigest, context.ParametersHash} {
		if digest == "" && digest != context.ParametersHash {
			continue
		}
		if len(digest) != sha256.Size*2 {
			return ErrAgentTransactionContext
		}
		bytes, err := hex.DecodeString(digest)
		if err != nil || len(bytes) != sha256.Size || strings.ToLower(digest) != digest {
			return ErrAgentTransactionContext
		}
	}
	return nil
}

// AgentTransactionParametersHash binds every additional significant parameter,
// including fees, destination, evidence reference, and expected business state.
// Only non-secret server-owned transaction data belongs in this digest.
func AgentTransactionParametersHash(parameters any) (string, error) {
	encoded, err := common.Marshal(parameters)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func NewAgentTransactionContext(action, requestID string, owner int, profileVersion, policyVersion int64, currency, minor string, quota int64, targetType, targetID string, targetVersion int64, parameters any) (AgentTransactionContext, error) {
	hash, err := AgentTransactionParametersHash(parameters)
	if err != nil {
		return AgentTransactionContext{}, err
	}
	context := AgentTransactionContext{Action: action, RequestID: requestID, AgentUserID: owner, ProfileVersion: profileVersion, PolicyVersion: policyVersion, Currency: currency, AmountMinor: minor, Quota: quota, TargetType: targetType, TargetID: targetID, TargetVersion: targetVersion, ParametersHash: hash}
	return context, ValidateAgentTransactionContext(context)
}

func AgentTransactionContextHash(context AgentTransactionContext) (string, error) {
	if err := ValidateAgentTransactionContext(context); err != nil {
		return "", err
	}
	encoded, err := common.Marshal(context)
	if err != nil {
		return "", err
	}
	return common.GenerateHMACWithKey([]byte("agent-transaction-context-v1:"+common.SessionSecret), string(encoded)), nil
}

type agentTransactionProofPayload struct {
	Identity    AuthSessionIdentity `json:"identity"`
	ContextHash string              `json:"context_hash"`
	Method      string              `json:"method"`
}

// AgentTransactionAuthorization cannot be populated by JSON request binding.
// Its token is not a reusable permission receipt: the database claim is made
// only inside the transaction that performs the exact financial operation.
type AgentTransactionAuthorization struct {
	identity    AuthSessionIdentity
	token       string
	contextHash string
}

func (authorization AgentTransactionAuthorization) Identity() AuthSessionIdentity {
	return authorization.identity
}

func requireAgentTransactionFactorTx(tx *gorm.DB, identity AuthSessionIdentity, method string) error {
	if _, pat := ParseAccessTokenSessionID(identity.SessionID); pat || identity.SessionID == "" {
		return ErrAgentTransactionProof
	}
	state, err := getUserVerificationState(tx, identity.UserID, true)
	if err != nil {
		return err
	}
	if state.Status != common.UserStatusEnabled || state.AuthVersion != identity.UserAuthVersion {
		return ErrAgentTransactionProof
	}
	switch method {
	case "2fa":
		if !state.HasTwoFA || state.TwoFALocked {
			return ErrAgentTransactionMethod
		}
	case "passkey":
		if !state.HasPasskey || !system_setting.PasskeySettingsSnapshot().Enabled {
			return ErrAgentTransactionMethod
		}
	default:
		return ErrAgentTransactionMethod
	}
	return ValidateAuthSessionWithTx(tx, identity)
}

// IssueAgentTransactionProof is called only after a fresh TOTP or UV-required
// Passkey ceremony. It stores no OTP, private credential, or plaintext token.
func IssueAgentTransactionProof(identity AuthSessionIdentity, contextHash, method string) (string, int64, error) {
	if len(contextHash) != 64 {
		return "", 0, ErrAgentTransactionContext
	}
	expiresAt := time.Now().Add(AgentTransactionProofTTL).Truncate(time.Second)
	var token string
	err := DB.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).Transaction(func(tx *gorm.DB) error {
		if err := requireAgentTransactionFactorTx(tx, identity, method); err != nil {
			return err
		}
		payload, err := common.Marshal(agentTransactionProofPayload{Identity: identity, ContextHash: contextHash, Method: method})
		if err != nil {
			return err
		}
		token, _, err = createAuthFlowWithTx(tx, AuthFlowCreate{Purpose: agentTransactionFlowPurpose, UserId: identity.UserID, SessionId: identity.SessionID, Payload: string(payload), ExpiresAt: expiresAt})
		return err
	})
	return token, expiresAt.Unix(), err
}

func PrepareAgentTransactionAuthorization(raw string, identity AuthSessionIdentity, context AgentTransactionContext) (AgentTransactionAuthorization, error) {
	if _, pat := ParseAccessTokenSessionID(identity.SessionID); pat || identity.SessionID == "" || identity.UserID <= 0 || identity.SessionVersion <= 0 || identity.UserAuthVersion < 0 || len(raw) != 43 {
		return AgentTransactionAuthorization{}, ErrAgentTransactionProof
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || len(decoded) != 32 {
		return AgentTransactionAuthorization{}, ErrAgentTransactionProof
	}
	hash, err := AgentTransactionContextHash(context)
	if err != nil {
		return AgentTransactionAuthorization{}, err
	}
	return AgentTransactionAuthorization{identity: identity, token: raw, contextHash: hash}, nil
}

// ConsumeAgentTransactionAuthorizationTx must run in the caller's financial
// transaction. LOCK ORDER: current policy singleton FIRST, operation/draft,
// financial accounts/batches/slices, related users in ID order, profiles, then
// session/proof. Callers MUST lock policy before their business rows; repeating
// the policy lock here does not repair a caller's reversed lock order.
// expected must be reconstructed from locked authoritative business rows, not
// copied from client JSON. Separate creation/review actors are checked against
// the immutable business record by the caller. Rollback restores both money
// and proof together; a committed proof can never authorize another operation.
func ConsumeAgentTransactionAuthorizationTx(tx *gorm.DB, authorization AgentTransactionAuthorization, expected AgentTransactionContext) error {
	if tx == nil || authorization.identity.UserID <= 0 || authorization.token == "" {
		return ErrAgentTransactionProof
	}
	hash, err := AgentTransactionContextHash(expected)
	if err != nil {
		return err
	}
	if !hmac.Equal([]byte(hash), []byte(authorization.contextHash)) {
		return ErrAgentTransactionProof
	}
	policy, err := LockCurrentAgentPolicyTx(tx)
	if err != nil {
		return err
	}
	if policy.ID != expected.PolicyVersion {
		return ErrAgentTransactionProof
	}
	identity := authorization.identity
	if _, pat := ParseAccessTokenSessionID(identity.SessionID); pat {
		return ErrAgentTransactionProof
	}
	rule, _ := agentTransactionActionRule(expected.Action)
	var users []User
	if err := lockForUpdate(tx).Where("id IN ?", []int{identity.UserID, expected.AgentUserID}).Order("id").Find(&users).Error; err != nil {
		return err
	}
	var actor, target *User
	for i := range users {
		if users[i].Id == identity.UserID {
			actor = &users[i]
		}
		if users[i].Id == expected.AgentUserID {
			target = &users[i]
		}
	}
	if actor == nil || target == nil || actor.Status != common.UserStatusEnabled {
		return ErrAgentProfilePermission
	}
	admin := actor.Role == common.RoleAdminUser || actor.Role == common.RoleRootUser
	if (rule.administrator || identity.UserID != expected.AgentUserID) && !admin {
		return ErrAgentProfilePermission
	}
	if rule.root && actor.Role != common.RoleRootUser {
		return ErrAgentProfilePermission
	}
	profile, err := LockAgentProfileTx(tx, expected.AgentUserID)
	if err != nil {
		return err
	}
	version := int64(0)
	if profile != nil {
		version = profile.Version
	}
	if version != expected.ProfileVersion {
		return ErrAgentProfileConflict
	}
	if profile == nil {
		if !rule.optionalProfile {
			return ErrAgentProfilePermission
		}
	} else if rule.capability != AgentCapabilityAdmin && !profile.Allows(rule.capability) {
		return ErrAgentProfilePermission
	}
	if rule.capability == AgentCapabilitySales && target.Status != common.UserStatusEnabled {
		return ErrAgentProfilePermission
	}
	if expected.Action == "commission.convert.procurement" && !profile.Allows(AgentCapabilitySales) {
		return ErrAgentProfilePermission
	}
	privateTx := tx.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	var flow AuthFlow
	match := AuthFlowMatch{Purpose: agentTransactionFlowPurpose, UserId: identity.UserID, SessionId: identity.SessionID}
	if err := applyAuthFlowMatch(privateTx, authorization.token, match).First(&flow).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrAgentTransactionProof
		}
		return err
	}
	var payload agentTransactionProofPayload
	if common.UnmarshalJsonStr(flow.Payload, &payload) != nil || payload.Identity != identity || !hmac.Equal([]byte(payload.ContextHash), []byte(hash)) {
		return ErrAgentTransactionProof
	}
	if err := requireAgentTransactionFactorTx(privateTx, identity, payload.Method); err != nil {
		return err
	}
	now := time.Now()
	if !flow.ExpiresAt.After(now) {
		return ErrAuthFlowExpired
	}
	result := applyAuthFlowMatch(privateTx.Model(&AuthFlow{}), authorization.token, match).Where("consumed_at IS NULL AND expires_at > ?", now).Update("consumed_at", now)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrAuthFlowConsumed
	}
	return nil
}
