package chuanapi

import (
	agentmodule "github.com/QuantumNous/new-api/modules/chuanapi/agents"
	"github.com/QuantumNous/new-api/pkg/lifecycle"
)

func StartRuntimeWorkers(workers *lifecycle.Group) { agentmodule.StartRuntimeWorkers(workers) }
