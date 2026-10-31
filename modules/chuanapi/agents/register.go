// Package agents owns the build-time registration boundary for the modular
// agent platform. It shares the host's authoritative ledgers and database;
// independent portals consume its session-protected HTTP API, not another ledger.
package agents

import (
	"fmt"

	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

const (
	ExtensionName = "chuanapi"
	ModelsName    = "agent_platform_models"
	MigrationName = "agent_platform_legacy_provenance"
	RoutesName    = "agent_platform_routes"
)

func Models() []any { return model.AgentPlatformModels() }

func Register(reg *extension.Registry) error {
	if reg == nil {
		return fmt.Errorf("agent platform: registry must not be nil")
	}
	if err := reg.RegisterModelSet(extension.ModelSet{Extension: ExtensionName, Name: ModelsName, Phase: extension.PhaseAfterCoreModels, Models: Models()}); err != nil {
		return err
	}
	if err := reg.RegisterDataStep(extension.DataStep{Extension: ExtensionName, Name: MigrationName, Phase: extension.PhaseAfterCoreData, Run: migrateLegacy}); err != nil {
		return err
	}
	return reg.RegisterRouteSet(extension.RouteSet{Extension: ExtensionName, Name: RoutesName, Priority: extension.PriorityBeforePlugin, RegisterScoped: registerRoutes})
}

func migrateLegacy(db *gorm.DB) error {
	if err := model.MigrateLegacyAgentProfiles(db); err != nil {
		return err
	}
	return model.MigrateLegacyAgentFundsWithDB(db)
}

func registerRoutes(scopes extension.RouteScopes) error {
	if scopes.AgentPlatformRoutes == nil {
		return fmt.Errorf("agent platform: host route registrar is unavailable")
	}
	return scopes.AgentPlatformRoutes(scopes)
}
