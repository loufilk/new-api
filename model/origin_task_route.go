package model

import (
	"context"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"gorm.io/gorm/clause"
)

// EnabledGroupsForOriginChannel reads only current authorization facts from
// the primary, not credentials or historical channel snapshots.
func EnabledGroupsForOriginChannel(ctx context.Context, id int, name string, groups []string) ([]string, error) {
	if id <= 0 || name == "" || len(groups) == 0 {
		return nil, nil
	}
	names := []string{name}
	if match := ratio_setting.RoutingMatchModelName(name); match != "" && match != name {
		names = append(names, match)
	}
	var result []string
	col := DB.Statement.Quote(clause.Column{Table: "abilities", Name: "group"})
	err := DB.WithContext(ctx).Table("abilities").Joins("JOIN channels ON channels.id = abilities.channel_id").
		Where("abilities.channel_id = ? AND abilities.enabled = ? AND channels.status = ?", id, true, common.ChannelStatusEnabled).
		Where("abilities.model IN ? AND "+col+" IN ?", names, groups).Distinct().Pluck(col, &result).Error
	return result, err
}
