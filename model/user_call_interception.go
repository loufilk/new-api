package model

import (
	"errors"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const MaxUserCallInterceptionReplyBytes = 16000

const (
	UserCallInterceptionIdentityAPIKey             = "api_key"
	UserCallInterceptionIdentityDashboardSession   = "dashboard_session"
	UserCallInterceptionIdentityArtifactCapability = "artifact_capability"
	UserCallInterceptionIdentityPublicArtifact     = "public_artifact"
)

var (
	ErrCallInterceptionReplyRequired = errors.New("A custom reply is required when call interception is enabled")
	ErrCallInterceptionReplyTooLong  = errors.New("Custom reply must not exceed 16000 UTF-8 bytes")
	ErrCallInterceptionReplyNull     = errors.New("Custom reply must not contain null characters")
	ErrCallInterceptionConflict      = errors.New("Call interception configuration changed; reload and try again")
	ErrCallInterceptionInvalid       = errors.New("Invalid parameters")
	ErrCallInterceptionPermission    = errors.New("Unauthorized, insufficient privileges")
)

// Kept separate from User deliberately: neither profile updates nor generic user
// binding can enable/disable interception. No credential, balance or login state
// is changed by this policy.
type UserCallInterception struct {
	UserID    int    `json:"user_id" gorm:"primaryKey;autoIncrement:false"`
	Enabled   bool   `json:"enabled" gorm:"not null"`
	Reply     string `json:"reply" gorm:"type:text;not null"`
	Revision  int64  `json:"revision" gorm:"not null"`
	UpdatedAt int64  `json:"updated_at" gorm:"autoUpdateTime:false;not null"`
	UpdatedBy int    `json:"updated_by" gorm:"not null"`
}

// Hit records live in the primary database, never LOG_DB/consume logs. Status is
// the response selected by the gateway; persistence precedes writing its bytes
// and is not a receipt that a disconnected client consumed the whole stream.
type UserCallInterceptionHit struct {
	ID           int64  `json:"id" gorm:"primaryKey;index:idx_uci_hit_user_time,priority:3"`
	Time         int64  `json:"time" gorm:"not null;index:idx_uci_hit_user_time,priority:2"`
	UserID       int    `json:"user_id" gorm:"not null;index:idx_uci_hit_user_time,priority:1"`
	ActorUserID  int    `json:"actor_user_id" gorm:"not null"`
	IdentityKind string `json:"identity_kind" gorm:"type:varchar(32);not null"`
	TokenID      int    `json:"token_id" gorm:"not null"`
	RequestID    string `json:"request_id" gorm:"type:varchar(128);not null;index:idx_uci_hit_request"`
	Interface    string `json:"interface" gorm:"type:varchar(255);not null"`
	Protocol     string `json:"protocol" gorm:"type:varchar(32);not null"`
	Status       int    `json:"status" gorm:"not null"`
	Revision     int64  `json:"revision" gorm:"not null"`
}

// Do not add reply text, hashes, request bodies or credentials to this schema.
// The configuration and this minimal audit are written in the same transaction.
type UserCallInterceptionAudit struct {
	ID              int64  `json:"id" gorm:"primaryKey;index:idx_uci_audit_user_time,priority:3"`
	Time            int64  `json:"time" gorm:"not null;index:idx_uci_audit_user_time,priority:2"`
	UserID          int    `json:"user_id" gorm:"not null;index:idx_uci_audit_user_time,priority:1"`
	ActorID         int    `json:"actor_id" gorm:"not null"`
	RequestID       string `json:"request_id" gorm:"type:varchar(128);not null;index:idx_uci_audit_request"`
	PreviousEnabled bool   `json:"previous_enabled" gorm:"not null"`
	Enabled         bool   `json:"enabled" gorm:"not null"`
	ReplyChanged    bool   `json:"reply_changed" gorm:"not null"`
	Revision        int64  `json:"revision" gorm:"not null"`
}

type UserCallInterceptionUpdate struct {
	Enabled          bool
	Reply            string
	ExpectedRevision int64
	RequestID        string
}

func ValidateUserCallInterceptionReply(enabled bool, reply string) error {
	if !utf8.ValidString(reply) {
		return ErrCallInterceptionInvalid
	}
	// PostgreSQL TEXT rejects U+0000; reject it consistently before persistence
	// on every dialect, including when saving a disabled draft.
	if strings.IndexByte(reply, 0) >= 0 {
		return ErrCallInterceptionReplyNull
	}
	if len(reply) > MaxUserCallInterceptionReplyBytes {
		return ErrCallInterceptionReplyTooLong
	}
	if enabled && !strings.ContainsFunc(reply, func(r rune) bool {
		return !unicode.IsSpace(r) && !unicode.IsControl(r) && !unicode.Is(unicode.Cf, r)
	}) {
		return ErrCallInterceptionReplyRequired
	}
	return nil
}

// This primary-DB lookup intentionally has no process/Redis cache. A request
// whose lookup starts after a save commits observes that save on every node.
// Requests already past their lookup keep their snapshot; no long-lived relay
// is retroactively canceled by changing this policy.
func GetUserCallInterception(userID int) (*UserCallInterception, error) {
	if userID <= 0 {
		return nil, ErrCallInterceptionInvalid
	}
	config := &UserCallInterception{UserID: userID}
	err := DB.Session(&gorm.Session{Logger: DB.Logger.LogMode(gormlogger.Silent)}).Where("user_id = ?", userID).Take(config).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return config, nil
	}
	return config, err
}

func SaveUserCallInterception(actorID, userID int, update UserCallInterceptionUpdate) (*UserCallInterception, error) {
	if actorID <= 0 || userID <= 0 || update.ExpectedRevision < 0 || !validCallInterceptionMetadata(update.RequestID, 128) {
		return nil, ErrCallInterceptionInvalid
	}
	if err := ValidateUserCallInterceptionReply(update.Enabled, update.Reply); err != nil {
		return nil, err
	}
	var result UserCallInterception
	// SQL diagnostics can contain the stored reply. Never let the ORM logger
	// interpolate these statements, even when debug logging is enabled.
	err := DB.Session(&gorm.Session{Logger: DB.Logger.LogMode(gormlogger.Silent)}).Transaction(func(tx *gorm.DB) error {
		var users []User
		if err := lockForUpdate(tx).Select("id", "role", "status").Where("id IN ?", []int{actorID, userID}).Order("id").Find(&users).Error; err != nil {
			return err
		}
		var actor, target *User
		for i := range users {
			if users[i].Id == actorID {
				actor = &users[i]
			}
			if users[i].Id == userID {
				target = &users[i]
			}
		}
		if actor == nil || actor.Status != common.UserStatusEnabled || actor.Role < common.RoleAdminUser || !common.IsValidateRole(actor.Role) {
			return ErrCallInterceptionPermission
		}
		if target == nil {
			return gorm.ErrRecordNotFound
		}
		if actor.Role != common.RoleRootUser && actor.Role <= target.Role {
			return ErrCallInterceptionPermission
		}
		previous := UserCallInterception{UserID: userID}
		err := tx.Where("user_id = ?", userID).Take(&previous).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if previous.Revision != update.ExpectedRevision || previous.Revision == math.MaxInt64 {
			return ErrCallInterceptionConflict
		}
		result = UserCallInterception{UserID: userID, Enabled: update.Enabled, Reply: update.Reply, Revision: previous.Revision + 1, UpdatedAt: common.GetTimestamp(), UpdatedBy: actorID}
		if previous.Revision == 0 {
			if err := tx.Create(&result).Error; err != nil {
				return err
			}
		} else {
			updated := tx.Model(&UserCallInterception{}).Where("user_id = ? AND revision = ?", userID, previous.Revision).Updates(map[string]any{"enabled": result.Enabled, "reply": result.Reply, "revision": result.Revision, "updated_at": result.UpdatedAt, "updated_by": actorID})
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected != 1 {
				return ErrCallInterceptionConflict
			}
		}
		return tx.Create(&UserCallInterceptionAudit{Time: result.UpdatedAt, UserID: userID, ActorID: actorID, RequestID: update.RequestID, PreviousEnabled: previous.Enabled, Enabled: result.Enabled, ReplyChanged: previous.Reply != result.Reply, Revision: result.Revision}).Error
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func validCallInterceptionMetadata(value string, maxBytes int) bool {
	return value != "" && len(value) <= maxBytes && utf8.ValidString(value) && !strings.ContainsFunc(value, unicode.IsControl)
}

func RecordUserCallInterceptionHit(hit *UserCallInterceptionHit) error {
	if hit == nil || hit.UserID <= 0 || hit.TokenID < 0 || hit.Revision <= 0 || hit.Status < 100 || hit.Status > 599 || !validCallInterceptionMetadata(hit.RequestID, 128) || !validCallInterceptionMetadata(hit.Interface, 255) || !validCallInterceptionMetadata(hit.Protocol, 32) {
		return ErrCallInterceptionInvalid
	}
	// Ownership of a public/signed artifact is not authentication of its viewer.
	// These values are established by server handlers, never request fields.
	switch hit.IdentityKind {
	case UserCallInterceptionIdentityAPIKey:
		if hit.ActorUserID != hit.UserID || hit.TokenID <= 0 {
			return ErrCallInterceptionInvalid
		}
	case UserCallInterceptionIdentityDashboardSession:
		// An authorized administrator may view another user's artifact. Keep
		// that authenticated actor distinct from the policy/asset owner.
		if hit.ActorUserID <= 0 || hit.TokenID != 0 {
			return ErrCallInterceptionInvalid
		}
	case UserCallInterceptionIdentityArtifactCapability, UserCallInterceptionIdentityPublicArtifact:
		if hit.ActorUserID != 0 || hit.TokenID != 0 {
			return ErrCallInterceptionInvalid
		}
	default:
		return ErrCallInterceptionInvalid
	}
	if hit.Time == 0 {
		hit.Time = common.GetTimestamp()
	}
	return DB.Session(&gorm.Session{Logger: DB.Logger.LogMode(gormlogger.Silent)}).Create(hit).Error
}

type UserCallInterceptionLogFilter struct {
	UserID       int
	IdentityKind string
	ActorUserID  *int
	TokenID      *int
	ActorID      *int
	RequestID    string
	Protocol     string
	Status       int
	StartTime    int64
	EndTime      int64
}

func callInterceptionLogQuery(db *gorm.DB, filter UserCallInterceptionLogFilter) *gorm.DB {
	db = db.Where("user_id = ?", filter.UserID)
	if filter.RequestID != "" {
		db = db.Where("request_id = ?", filter.RequestID)
	}
	if filter.StartTime > 0 {
		db = db.Where("time >= ?", filter.StartTime)
	}
	if filter.EndTime > 0 {
		db = db.Where("time <= ?", filter.EndTime)
	}
	return db
}

func GetUserCallInterceptionHits(filter UserCallInterceptionLogFilter, offset, limit int) ([]UserCallInterceptionHit, int64, error) {
	rows := make([]UserCallInterceptionHit, 0)
	if filter.UserID <= 0 || offset < 0 || limit < 1 || limit > 100 {
		return rows, 0, ErrCallInterceptionInvalid
	}
	query := callInterceptionLogQuery(DB.Model(&UserCallInterceptionHit{}), filter)
	if filter.IdentityKind != "" {
		query = query.Where("identity_kind = ?", filter.IdentityKind)
	}
	if filter.ActorUserID != nil {
		query = query.Where("actor_user_id = ?", *filter.ActorUserID)
	}
	if filter.TokenID != nil {
		query = query.Where("token_id = ?", *filter.TokenID)
	}
	if filter.Protocol != "" {
		query = query.Where("protocol = ?", filter.Protocol)
	}
	if filter.Status != 0 {
		query = query.Where("status = ?", filter.Status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return rows, 0, err
	}
	err := query.Order("time DESC, id DESC").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, total, err
}

func GetUserCallInterceptionAudits(filter UserCallInterceptionLogFilter, offset, limit int) ([]UserCallInterceptionAudit, int64, error) {
	rows := make([]UserCallInterceptionAudit, 0)
	if filter.UserID <= 0 || offset < 0 || limit < 1 || limit > 100 {
		return rows, 0, ErrCallInterceptionInvalid
	}
	query := callInterceptionLogQuery(DB.Model(&UserCallInterceptionAudit{}), filter)
	if filter.ActorID != nil {
		query = query.Where("actor_id = ?", *filter.ActorID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return rows, 0, err
	}
	err := query.Order("time DESC, id DESC").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, total, err
}
