package model

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	laneCheckoutIssuerRelation   = "lane_checkout_issuer_v1"
	laneCheckoutIdentityRelation = "lane_checkout_identities_v1"
	laneCheckoutEpayTopup        = "epay_topup"
	laneCheckoutEpaySubscription = "epay_subscription"
)

var (
	errLaneCheckoutIssuer   = fmt.Errorf("%w: checkout issuer is not enrolled for this generation", ErrLaneStoreAuthorityEvidence)
	errLaneCheckoutConflict = fmt.Errorf("%w: checkout identity conflict", ErrLaneEpayExclusiveOrder)
)

// There is no runtime issuer, registry migration, or checkout-route caller
// for this capability. A public request, epoch, or nil authority cannot
// produce it. The identifier and generation are pinned to the same store.
type laneCheckoutNamespaceV1 struct {
	primary                 *gorm.DB
	pool                    *sql.DB
	space                   laneCustodySpaceV1
	roster                  string
	issuerID                string
	generation, walletEpoch int64
	signer                  *laneEpayOrderSigner
	custody                 laneEpayTxAuthority
}

type laneCheckoutIssuerV1 struct {
	Singleton      int    `gorm:"primaryKey;autoIncrement:false"`
	IssuerID       string `gorm:"type:varchar(64);not null"`
	Generation     int64  `gorm:"not null"`
	WalletEpoch    int64  `gorm:"not null"`
	KeyID          string `gorm:"type:varchar(64);not null"`
	KeyFingerprint string `gorm:"type:varchar(64);not null"`
}

func (laneCheckoutIssuerV1) TableName() string { return laneCheckoutIssuerRelation }

// A request ID is chosen once by a trusted caller before any provider form.
// Both IDs are unique across participating families, not per family/user.
type laneCheckoutIdentityV1 struct {
	TradeNo                 string `gorm:"primaryKey;type:varchar(64)"`
	RequestID               string `gorm:"type:varchar(64);not null;uniqueIndex"`
	Family, IssuerID        string `gorm:"type:varchar(64);not null"`
	Generation, WalletEpoch int64  `gorm:"not null"`
	UserID                  int    `gorm:"not null"`
	QuoteSHA256, KeyID, MAC string `gorm:"type:varchar(64);not null"`
}

func (laneCheckoutIdentityV1) TableName() string            { return laneCheckoutIdentityRelation }
func (*laneCheckoutIdentityV1) BeforeUpdate(*gorm.DB) error { return ErrLedgerImmutable }
func (*laneCheckoutIdentityV1) BeforeDelete(*gorm.DB) error { return ErrLedgerImmutable }

type laneCheckoutRequestV1 struct {
	tradeNo, requestID, family, quote string
	userID                            int
}

func (n *laneCheckoutNamespaceV1) physicalTx(tx *gorm.DB, primary *gorm.DB, scope laneEpaySettlementScope) error {
	if n == nil || tx == nil || n.primary == nil || n.primary != primary || scope.db != primary || n.pool == nil ||
		n.walletEpoch <= 0 || n.walletEpoch != scope.walletEpoch || n.generation <= 0 ||
		!n.signer.valid() || !laneBillingOwnerIDValid(n.issuerID) || n.space.Schema != scope.schema {
		return errLaneCheckoutIssuer
	}
	pool, err := tx.DB()
	if err != nil || pool != n.pool {
		return errLaneCheckoutIssuer
	}
	space, err := laneCustodySpaceTx(tx)
	if err != nil || space != n.space {
		return errLaneCheckoutIssuer
	}
	roster, err := readLaneCheckoutRosterTx(tx, space)
	if err != nil {
		return err
	}
	if roster != n.roster {
		return errLaneCheckoutIssuer
	}
	return nil
}

func readLaneCheckoutRosterTx(tx *gorm.DB, space laneCustodySpaceV1) (string, error) {
	var roster string
	names := []string{"top_ups", "subscription_orders", "lane_epay_order_bindings_v1", laneCheckoutIssuerRelation, laneCheckoutIdentityRelation}
	err := tx.Raw(`SELECT coalesce(string_agg(c.relname||':'||c.oid::text,',' ORDER BY c.relname),'')
		FROM pg_catalog.pg_class c WHERE c.relnamespace=?::oid AND c.relname IN ?
		AND c.relkind='r' AND c.relpersistence='p'`, space.SchemaOID, names).Row().Scan(&roster)
	if err != nil {
		return "", err
	}
	if strings.Count(roster, ",") != len(names)-1 {
		return "", errLaneCheckoutIssuer
	}
	// PostgreSQL must enforce both independent reservations, not merely
	// accept GORM tags. Neither column can be replaced by a partial,
	// deferred, expression, or included-column unique index.
	var keys int
	err = tx.Raw(`SELECT COUNT(DISTINCT a.attname)
		FROM pg_catalog.pg_index i JOIN pg_catalog.pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=i.indkey[0]
		WHERE i.indrelid=?::regclass AND a.attname IN ('trade_no','request_id') AND a.attnotnull
		AND i.indisunique AND i.indisvalid AND i.indisready AND i.indimmediate
		AND i.indnatts=1 AND i.indexprs IS NULL AND i.indpred IS NULL`,
		laneStoreQualified(space.Schema, laneCheckoutIdentityRelation)).Row().Scan(&keys)
	if err != nil {
		return "", err
	}
	if keys != 2 {
		return "", errLaneCheckoutIssuer
	}
	return roster, nil
}

func (n *laneCheckoutNamespaceV1) authorityTx(tx *gorm.DB, primary *gorm.DB, scope laneEpaySettlementScope) error {
	if err := n.physicalTx(tx, primary, scope); err != nil {
		return err
	}
	if n.custody == nil || LOG_DB != primary {
		return errLaneCheckoutIssuer
	}
	if err := n.custody.CheckTx(tx, primary, LOG_DB, scope.walletEpoch); err != nil {
		return err
	}
	var issuer laneCheckoutIssuerV1
	err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("singleton = ?", 1).Take(&issuer).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errLaneCheckoutIssuer
	}
	if err != nil {
		return err
	}
	if issuer.IssuerID != n.issuerID || issuer.Generation != n.generation ||
		issuer.WalletEpoch != n.walletEpoch || issuer.KeyID != n.signer.keyID ||
		!hmac.Equal([]byte(issuer.KeyFingerprint), []byte(laneCheckoutKeyFingerprint(n.signer))) {
		return errLaneCheckoutIssuer
	}
	return nil
}

func laneCheckoutKeyFingerprint(s *laneEpayOrderSigner) string {
	keyHash := sha256.Sum256(s.key[:])
	return hex.EncodeToString(keyHash[:])
}

func (n *laneCheckoutNamespaceV1) signedIdentity(r laneCheckoutRequestV1) (laneCheckoutIdentityV1, error) {
	if n == nil || !n.signer.valid() || !laneBillingOwnerIDValid(r.tradeNo) || !laneBillingOwnerIDValid(r.requestID) || r.userID <= 0 ||
		(r.family != laneCheckoutEpayTopup && r.family != laneCheckoutEpaySubscription) || !laneCheckoutDigest(r.quote) {
		return laneCheckoutIdentityV1{}, errLaneCheckoutConflict
	}
	raw, err := common.Marshal([]any{"lane-checkout-identity-v1", n.space.Schema, n.space.DatabaseOID, n.space.SchemaOID,
		n.issuerID, n.generation, n.walletEpoch, n.signer.keyID, r.tradeNo, r.requestID, r.family, r.userID, r.quote})
	if err != nil {
		return laneCheckoutIdentityV1{}, err
	}
	mac := hmac.New(sha256.New, n.signer.key[:])
	_, _ = mac.Write(raw)
	return laneCheckoutIdentityV1{TradeNo: r.tradeNo, RequestID: r.requestID, Family: r.family, UserID: r.userID,
		IssuerID: n.issuerID, Generation: n.generation, WalletEpoch: n.walletEpoch, QuoteSHA256: r.quote, KeyID: n.signer.keyID,
		MAC: hex.EncodeToString(mac.Sum(nil))}, nil
}

func laneCheckoutDigest(s string) bool {
	data, err := hex.DecodeString(s)
	return err == nil && len(data) == sha256.Size && s == strings.ToLower(s)
}

func laneCheckoutIdentityMatches(have, want laneCheckoutIdentityV1) bool {
	if !hmac.Equal([]byte(have.MAC), []byte(want.MAC)) {
		return false
	}
	have.MAC, want.MAC = "", ""
	return have == want
}

// Acquire the canonical trade/request reservation before touching any family
// order or wallet. A cross-family user must call this under the same authority
// and transaction, before its own order/write locks. Raw legacy routes do
// not participate; live writer fencing is separate from this safe contract.
func (n *laneCheckoutNamespaceV1) allocateTx(tx *gorm.DB, primary *gorm.DB, scope laneEpaySettlementScope, r laneCheckoutRequestV1) (laneCheckoutIdentityV1, bool, error) {
	var empty laneCheckoutIdentityV1
	if err := n.authorityTx(tx, primary, scope); err != nil {
		return empty, false, err
	}
	if scope.userID != r.userID || scope.tradeNo != r.tradeNo {
		return empty, false, errLaneCheckoutConflict
	}
	want, err := n.signedIdentity(r)
	if err != nil {
		return empty, false, err
	}
	inserted := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&want)
	if inserted.Error != nil {
		return empty, false, inserted.Error
	}
	if inserted.RowsAffected == 0 {
		var matches []laneCheckoutIdentityV1
		err := lockForUpdate(tx).Where("trade_no = ? OR request_id = ?", r.tradeNo, r.requestID).
			Order("trade_no").Limit(2).Find(&matches).Error
		if err != nil {
			return empty, false, err
		}
		if len(matches) != 1 || !laneCheckoutIdentityMatches(matches[0], want) {
			return empty, false, errLaneCheckoutConflict
		}
		return matches[0], true, nil
	}
	if inserted.RowsAffected != 1 {
		return empty, false, errLaneCheckoutConflict
	}
	// A prior legacy family row is not authorization and is never adopted.
	// The caller must roll back this transaction on any rejection.
	var existing TopUp
	err = lockForUpdate(tx).Select("id").Where("trade_no = ?", r.tradeNo).Take(&existing).Error
	if err == nil {
		return empty, false, errLaneCheckoutConflict
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return empty, false, err
	}
	var subscriptions []SubscriptionOrder
	if err := tx.Select("id").Where("trade_no = ?", r.tradeNo).Limit(1).Find(&subscriptions).Error; err != nil {
		return empty, false, err
	}
	if len(subscriptions) != 0 {
		return empty, false, errLaneCheckoutConflict
	}
	return want, false, nil
}

func (n *laneCheckoutNamespaceV1) verifyTx(tx *gorm.DB, primary *gorm.DB, scope laneEpaySettlementScope, r laneCheckoutRequestV1) error {
	// Capture/reconciliation verifies the immutable historical issuer
	// binding, not the current new-order issuer row. Rollover must retain
	// valid historical key material until all signed captures settle.
	if err := n.physicalTx(tx, primary, scope); err != nil {
		return err
	}
	if scope.userID != r.userID || scope.tradeNo != r.tradeNo {
		return errLaneCheckoutConflict
	}
	want, err := n.signedIdentity(r)
	if err != nil {
		return err
	}
	var stored laneCheckoutIdentityV1
	err = tx.Where("trade_no = ?", r.tradeNo).Take(&stored).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errLaneCheckoutConflict
	}
	if err != nil {
		return err
	}
	if !laneCheckoutIdentityMatches(stored, want) {
		return errLaneCheckoutConflict
	}
	return nil
}

func laneEpayCheckoutRequest(rid string, o TopUp) (laneCheckoutRequestV1, error) {
	raw, err := common.Marshal([]any{"epay-topup-quote-v1", o.PaymentProvider, o.PaymentMethod, o.Amount, o.Money,
		o.QuotedWalletQuota, o.QuotedPaymentMinor, o.QuotedPaymentCurrency, o.QuotedPaymentScope, o.QuotedPaymentEnvironment})
	if err != nil {
		return laneCheckoutRequestV1{}, err
	}
	sha := sha256.Sum256(raw)
	return laneCheckoutRequestV1{o.TradeNo, rid, laneCheckoutEpayTopup, hex.EncodeToString(sha[:]), o.UserId}, nil
}
