//go:build enterprise_lane_lab

package main

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/lifecycle"
)

func startEnterpriseLaneProjection(workers *lifecycle.Group) {
	if middleware.EnterpriseLaneGatewayEnabled() {
		workers.Go("enterprise-lane-usage-projection", model.RunEnterpriseLaneUsageProjection)
		if model.EnterpriseLaneTimingEnabled() {
			workers.Go("enterprise-lane-step-timing", model.RunEnterpriseLaneTiming)
		}
	}
}

func drainEnterpriseLaneProjection(ctx context.Context) error {
	if !middleware.EnterpriseLaneGatewayEnabled() {
		return nil
	}
	defer middleware.ReportEnterpriseLaneAdmissionWait()
	// Keep a separate bound so durable lab backlog cannot starve the ordinary
	// shutdown finalizers. Unfinished rows stay durable for the next lab boot.
	bounded, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	return model.DrainEnterpriseLaneUsageProjection(bounded, model.DB)
}
