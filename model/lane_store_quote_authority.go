package model

import (
	"crypto/sha256"
	"crypto/subtle"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// LaneStoreQuoteInputV1 takes identity from trusted gateway authorization,
// not client headers. Stage V1 is shadow authority for original finite Keys
// with narrow paid backing; it is not an owner, a funds hold, or live policy.
type LaneStoreQuoteInputV1 struct {
	Release                                                LaneStoreReleaseV1
	RequestID                                              string
	UserID, TokenID                                        int
	TokenFingerprint                                       string `json:"-"`
	UserAuthVersion, UserCacheRevision, TokenCacheRevision int64
	Group                                                  string
	GroupConfigRevision                                    int64
}

// LaneStoreQuoteAuthorityV1 is opaque read-only authority metadata. A zero
// value is invalid, and even exact, current facts cannot activate this build.
type LaneStoreQuoteAuthorityV1 struct {
	input             LaneStoreQuoteInputV1
	schema, userGroup string
	settingsSHA256    [sha256.Size]byte
}

func (q LaneStoreQuoteAuthorityV1) MatchesPreparedRequest(requestID, group string, revision int64) bool {
	return q.schema != "" && q.input.requestMatches(requestID, group, revision)
}

func (i LaneStoreQuoteInputV1) requestMatches(requestID, group string, revision int64) bool {
	return i.RequestID == requestID && i.Group == group && i.GroupConfigRevision == revision
}

func (i LaneStoreQuoteInputV1) valid() bool {
	return i.Release.valid() && laneBillingOwnerIDValid(i.RequestID) && i.UserID > 0 && i.TokenID > 0 &&
		laneStoreLowerHex(i.TokenFingerprint, 64) && i.UserAuthVersion > 0 && i.UserCacheRevision > 0 && i.TokenCacheRevision > 0 &&
		laneBillingOwnerIDValid(i.Group) && i.Group != "auto" && i.GroupConfigRevision >= 0
}

// CaptureLaneStoreQuoteAuthorityV1Tx does only bounded locking reads. Do not
// hold this transaction open while preparing a body, waiting, or sending
// HTTP. A later binding transaction must revalidate all captured facts.
func CaptureLaneStoreQuoteAuthorityV1Tx(tx *gorm.DB, input LaneStoreQuoteInputV1) (LaneStoreQuoteAuthorityV1, error) {
	return lockLaneStoreQuoteAuthorityV1Tx(tx, input, 0)
}

// RevalidateLaneStoreQuoteAuthorityV1Tx must run within the same eventual
// canonical owner transaction, before any lane financial writes. In this
// build it proves current dormant metadata and original paid/key raw backing
// only, never wallet escrow or permission to bind. No result is a hold.
func RevalidateLaneStoreQuoteAuthorityV1Tx(tx *gorm.DB, q LaneStoreQuoteAuthorityV1, capQuota int) error {
	if q.schema == "" || capQuota <= 0 || capQuota > common.MaxQuota {
		return ErrLaneStoreAuthorityEvidence
	}
	now, err := lockLaneStoreQuoteAuthorityV1Tx(tx, q.input, capQuota)
	if err != nil {
		return err
	}
	if now != q {
		return ErrLaneStoreAuthorityEvidence
	}
	return nil
}

type laneStoreAuthorityUser struct {
	ID, Status                                int
	AuthVersion, CacheRevision                int64
	Quota, QuotaPaid, QuotaBonus, QuotaReward int
	Group, Setting                            string
	Live                                      bool
}

type laneStoreAuthorityToken struct {
	ID, UserID, Status                                  int
	CacheRevision, ExpiredTime                          int64
	RemainQuota                                         int
	Key, Group                                          string
	Live, UnlimitedQuota, SmartRouting, CrossGroupRetry bool
}

// Acquire existing group namespace SHARE, store epoch SHARE, then original
// user and token SHARE. Known group writers use namespace→user→key. Existing
// wallet and Key mutations require row write locks. Production reservation
// and new writer activation require the common order, escrow conservation,
// complete writer inventory and an independent immutable route authority.
func lockLaneStoreQuoteAuthorityV1Tx(tx *gorm.DB, input LaneStoreQuoteInputV1, capQuota int) (LaneStoreQuoteAuthorityV1, error) {
	var empty LaneStoreQuoteAuthorityV1
	if !input.valid() {
		return empty, ErrLaneStoreAuthorityEvidence
	}
	if tx == nil || tx.Config == nil || tx.Statement == nil || tx.Statement.Context == nil || tx.Dialector == nil || tx.DryRun ||
		tx.Dialector.Name() != "postgres" || !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		return empty, ErrLaneStoreAuthorityUnavailable
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return empty, ErrLaneStoreAuthorityUnavailable
	}
	ctx := tx.Statement.Context
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > laneBillingOwnerTimeout || ctx.Err() != nil {
		return empty, ErrLaneStoreAuthorityUnavailable
	}
	tx = tx.Session(&gorm.Session{NewDB: true, Logger: logger.Discard, SkipHooks: true})
	var isolated bool
	// Avoid temporary or second-schema financial/option table resolution.
	if err := tx.Raw(`SELECT pg_catalog.current_schemas(false) = ARRAY[pg_catalog.current_schema()]::name[] AND pg_catalog.pg_my_temp_schema() = 0`).Row().Scan(&isolated); err != nil || !isolated {
		return empty, ErrLaneStoreAuthorityUnavailable
	}
	if err := tx.Exec("SELECT pg_catalog.pg_advisory_xact_lock_shared(?)", groupNamespaceAdvisoryLockID).Error; err != nil {
		return empty, laneStoreAuthorityError(ctx, err)
	}
	c, err := inspectLaneStoreCatalogTx(tx)
	if err != nil {
		return empty, err
	}
	row, err := readLaneStoreAuthorityRowTx(tx, c, "SHARE")
	if err != nil {
		return empty, err
	}
	if row.State != laneStoreAuthorityDormant || row.LaneStoreReleaseV1 != input.Release {
		return empty, ErrLaneStoreAuthorityEvidence
	}
	if UserGroupPricingEnabled() || validateGroupConfigVersionTx(tx, &input.GroupConfigRevision) != nil {
		return empty, ErrLaneStoreAuthorityEvidence
	}
	var user laneStoreAuthorityUser
	userResult := tx.Raw("SELECT id,status,auth_version,cache_revision,quota,quota_paid,quota_bonus,quota_reward,"+
		`"group",setting,deleted_at IS NULL AS live FROM `+laneStoreQualified(c.schema, "users")+" WHERE id = ? FOR SHARE", input.UserID).Scan(&user)
	if userResult.Error != nil {
		return empty, laneStoreAuthorityError(ctx, userResult.Error)
	}
	if userResult.RowsAffected != 1 || user.ID != input.UserID || !user.Live || user.Status != common.UserStatusEnabled ||
		user.AuthVersion != input.UserAuthVersion || user.CacheRevision != input.UserCacheRevision ||
		user.QuotaPaid <= 0 || user.QuotaPaid > common.MaxWalletQuota || user.Quota != user.QuotaPaid ||
		user.QuotaBonus != 0 || user.QuotaReward != 0 || user.QuotaPaid < capQuota {
		return empty, ErrLaneStoreAuthorityEvidence
	}
	var setting dto.UserSetting
	if common.Unmarshal([]byte(user.Setting), &setting) != nil || setting.BillingPreference != "wallet_only" {
		return empty, ErrLaneStoreAuthorityEvidence
	}
	// Bonus-source writes serialize through this User's lock. Reject drift
	// between the canonical raw mirror and any still-available bonus provenance.
	var bonusAvailable bool
	if err := tx.Raw("SELECT EXISTS (SELECT 1 FROM "+laneStoreQualified(c.schema, "bonus_source_states")+
		" WHERE user_id = ? AND available IS DISTINCT FROM 0)", input.UserID).Row().Scan(&bonusAvailable); err != nil {
		return empty, laneStoreAuthorityError(ctx, err)
	}
	if bonusAvailable {
		return empty, ErrLaneStoreAuthorityEvidence
	}
	var token laneStoreAuthorityToken
	tokenResult := tx.Raw("SELECT id,user_id,status,cache_revision,expired_time,remain_quota,"+
		`"key","group",unlimited_quota,smart_routing,cross_group_retry,deleted_at IS NULL AS live FROM `+
		laneStoreQualified(c.schema, "tokens")+" WHERE id = ? FOR SHARE", input.TokenID).Scan(&token)
	if tokenResult.Error != nil {
		return empty, laneStoreAuthorityError(ctx, tokenResult.Error)
	}
	if tokenResult.RowsAffected != 1 || !token.Live || token.ID != input.TokenID || token.UserID != input.UserID ||
		token.Status != common.TokenStatusEnabled || token.CacheRevision != input.TokenCacheRevision || token.UnlimitedQuota ||
		token.Group != input.Group || token.SmartRouting || token.CrossGroupRetry || token.RemainQuota <= 0 || token.RemainQuota > common.MaxWalletQuota ||
		token.RemainQuota < capQuota || (token.ExpiredTime != -1 && token.ExpiredTime <= time.Now().Unix()) || token.Key == "" ||
		subtle.ConstantTimeCompare([]byte(BillingTokenFingerprint(token.Key)), []byte(input.TokenFingerprint)) != 1 {
		return empty, ErrLaneStoreAuthorityEvidence
	}
	return LaneStoreQuoteAuthorityV1{input: input, schema: c.schema, userGroup: user.Group, settingsSHA256: sha256.Sum256([]byte(user.Setting))}, nil
}
