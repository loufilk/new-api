package common

import (
	"context"
	"fmt"
	"os"
	"runtime/pprof"
	"time"

	"github.com/QuantumNous/new-api/pkg/lifecycle"
	"github.com/shirou/gopsutil/cpu"
)

// Monitor retains the embedded-host compatibility entry point.
func Monitor() { RunCPUProfileMonitor(context.Background(), context.Background()) }

func RunCPUProfileMonitor(schedule, work context.Context) {
	lifecycle.Periodic(schedule, work, func() time.Duration { return 30 * time.Second }, true, func(ctx context.Context) {
		percent, err := cpu.PercentWithContext(ctx, time.Second, false)
		if err != nil || len(percent) == 0 || percent[0] <= 80 {
			return
		}
		if err := os.MkdirAll("./pprof", 0700); err != nil {
			SysError("failed to create pprof directory: " + err.Error())
			return
		}
		f, err := os.Create("./pprof/" + fmt.Sprintf("cpu-%s.pprof", time.Now().Format("20060102150405")))
		if err != nil {
			SysError("failed to create pprof file: " + err.Error())
			return
		}
		defer f.Close()
		if err := pprof.StartCPUProfile(f); err != nil {
			SysError("failed to start pprof: " + err.Error())
			return
		}
		defer pprof.StopCPUProfile()
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
		case <-timer.C:
		}
	})
}
