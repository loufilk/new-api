package model

import (
	"encoding/hex"
	"errors"
	"strings"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const groupRateLimitBucketsOptionKey = "GroupRateLimitBuckets"

var groupRateLimitBuckets atomic.Pointer[map[string]string]

// GroupRateLimitBucket is an opaque accounting namespace, never an access
// alias. Callers must first authorize the current group and capture its limits.
// Existing installations retain their literal group counter keys until renamed.
func GroupRateLimitBucket(group string) string {
	if buckets := groupRateLimitBuckets.Load(); buckets != nil {
		if bucket, ok := (*buckets)[group]; ok {
			return bucket
		}
	}
	return group
}

func parseGroupRateLimitBuckets(raw string) (map[string]string, error) {
	buckets := map[string]string{}
	if raw == "" {
		return buckets, nil
	}
	if err := common.UnmarshalJsonStr(raw, &buckets); err != nil || buckets == nil {
		return nil, errors.New("invalid group rate limit bucket metadata")
	}
	seen := make(map[string]struct{}, len(buckets))
	for group, bucket := range buckets {
		_, groupErr := normalizeGroupRenameName(group)
		_, bucketErr := normalizeGroupRenameName(bucket)
		if strings.HasPrefix(bucket, "\x00renamed:") {
			suffix := strings.TrimPrefix(bucket, "\x00renamed:")
			decoded, err := hex.DecodeString(suffix)
			if err == nil && len(decoded) == 16 && suffix == strings.ToLower(suffix) {
				bucketErr = nil
			}
		}
		if groupErr != nil || bucketErr != nil {
			return nil, errors.New("invalid group rate limit bucket metadata")
		}
		if _, duplicate := seen[bucket]; duplicate {
			return nil, errors.New("ambiguous group rate limit bucket metadata")
		}
		seen[bucket] = struct{}{}
	}
	return buckets, nil
}

// recordGroupRateLimitBucketsTx preserves in-flight and used rate-limit slots
// across rename without SCAN/renaming Redis keys or resetting local counters.
// The retired name receives an isolated bucket, so explicitly reusing that name
// never borrows the renamed group's counter or budget. NUL cannot occur in a
// legal group name, making the generated namespace disjoint from legacy keys.
func recordGroupRateLimitBucketsTx(tx *gorm.DB, mappings []GroupRenameMapping) error {
	if len(mappings) == 0 {
		return nil
	}
	var option Option
	if err := tx.Where(&Option{Key: groupRateLimitBucketsOptionKey}).Limit(1).Find(&option).Error; err != nil {
		return err
	}
	buckets, err := parseGroupRateLimitBuckets(option.Value)
	if err != nil {
		return err
	}
	for _, mapping := range mappings {
		bucket := buckets[mapping.From]
		if bucket == "" {
			bucket = mapping.From
		}
		buckets[mapping.To] = bucket
		buckets[mapping.From] = "\x00renamed:" + common.GetUUID()
	}
	raw, err := common.Marshal(buckets)
	if err != nil {
		return err
	}
	option = Option{Key: groupRateLimitBucketsOptionKey, Value: string(raw)}
	return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value"})}).Create(&option).Error
}
