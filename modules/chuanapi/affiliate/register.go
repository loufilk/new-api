package affiliate

import (
	"fmt"

	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/model"
)

const (
	ExtensionName = "chuanapi"
	ModelsName    = "affiliate_models"
)

// Models is the ChuanAPI-owned invite/reward schema: one commission voucher per
// successful online balance top-up (first three per invited user).
func Models() []any {
	return []any{
		&model.ReferralCommission{},
	}
}

func RegisterSchema(reg *extension.Registry) error {
	if reg == nil {
		return fmt.Errorf("chuanapi affiliate: registry must not be nil")
	}
	return reg.RegisterModelSet(extension.ModelSet{
		Extension: ExtensionName,
		Name:      ModelsName,
		Phase:     extension.PhaseAfterCoreModels,
		Models:    Models(),
	})
}
