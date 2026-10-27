package common

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/pkg/lifecycle"
)

func RunSystemMonitor(schedule, work context.Context) {
	usage := newContainerUsage()
	var detector overloadDetector
	lifecycle.Periodic(schedule, work, func() time.Duration {
		return systemMonitorInterval
	}, true, func(context.Context) {
		config := GetPerformanceMonitorConfig()
		if !config.Enabled {
			detector = overloadDetector{}
			latestSystemStatus.Store(SystemStatus{SampledAt: time.Now()})
			return
		}
		status := collectSystemStatus(usage)
		detector.mark(&status, config)
		latestSystemStatus.Store(status)
	})
}
