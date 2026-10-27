package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/lifecycle"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/pkg/wsmanager"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
)

type applicationRuntime struct {
	gate          *lifecycle.Gate
	workers       *lifecycle.Group
	profileServer *http.Server
}

func newApplicationRuntime() *applicationRuntime {
	return &applicationRuntime{gate: lifecycle.NewGate(), workers: lifecycle.NewGroup()}
}

// Start runs after HTTP bind and all module/task registration. No lease can be
// claimed before the adaptor factory and handlers have been installed.
func (app *applicationRuntime) Start(balanceInterval time.Duration, enablePprof bool) {
	model.StartRuntimeWorkers(app.workers, common.SyncFrequency)
	controller.StartRuntimeWorkers(app.workers, balanceInterval)
	service.StartRuntimeWorkers(app.workers)
	app.workers.Go("websocket-invalidation", func(schedule, _ context.Context) { wsmanager.RunSubscriber(schedule) })
	app.workers.Go("system-monitor", common.RunSystemMonitor)
	app.workers.Go("performance-flush", perfmetrics.RunFlush)
	if common.SyncFrequency > 0 {
		app.workers.Go("authorization-policy-sync", func(schedule, work context.Context) {
			lifecycle.Periodic(schedule, work, func() time.Duration { return time.Duration(common.SyncFrequency) * time.Second }, false, func(context.Context) {
				if err := authz.ReloadPolicy(); err != nil {
					common.SysError("authorization policy refresh failed: " + common.MaskSensitiveLogInfo(err.Error()))
				}
			})
		})
	}
	if enablePprof {
		app.workers.Go("cpu-profile-monitor", common.RunCPUProfileMonitor)
		listener, err := net.Listen("tcp", "127.0.0.1:8005")
		if err != nil {
			common.SysError("failed to bind pprof listener: " + err.Error())
			return
		}
		app.profileServer = &http.Server{Handler: http.DefaultServeMux, ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 120 * time.Second}
		app.workers.Go("pprof-http", func(schedule, _ context.Context) {
			stopped := make(chan struct{})
			go func() {
				defer close(stopped)
				<-schedule.Done()
				_ = app.profileServer.Close()
			}()
			if err := app.profileServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				common.SysError("pprof server stopped: " + err.Error())
			}
			<-stopped
		})
	}
}

// Shutdown reports whether all DB/Redis producers were joined. The caller must
// not explicitly close shared pools underneath work that missed the hard bound.
func (app *applicationRuntime) Shutdown(server *http.Server) bool {
	app.gate.BeginDrain()
	app.workers.BeginDrain()
	socketsClosed := make(chan struct{})
	go func() { defer close(socketsClosed); wsmanager.Shutdown() }()

	timeout := common.GetEnvOrDefault("SHUTDOWN_TIMEOUT_SECONDS", 120)
	if timeout <= 0 || timeout > 3600 {
		timeout = 120
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	if err := server.Shutdown(ctx); err != nil {
		common.SysError("HTTP graceful shutdown deadline reached")
	}
	requestsErr := app.gate.Wait(ctx)
	workersErr := app.workers.Wait(ctx)
	joined := requestsErr == nil && !errors.Is(workersErr, context.DeadlineExceeded) && !errors.Is(workersErr, context.Canceled)
	select {
	case <-socketsClosed:
	case <-ctx.Done():
		joined = false
	}
	cancel()
	if workersErr != nil {
		common.SysError("runtime worker shutdown: " + workersErr.Error())
	}
	if !joined {
		app.workers.ForceCancel()
		_ = server.Close()
		forced, stop := context.WithTimeout(context.Background(), 10*time.Second)
		requestsErr = app.gate.Wait(forced)
		workersErr = app.workers.Wait(forced)
		joined = requestsErr == nil && !errors.Is(workersErr, context.DeadlineExceeded) && !errors.Is(workersErr, context.Canceled)
		select {
		case <-socketsClosed:
		case <-forced.Done():
			joined = false
		}
		stop()
		if !joined {
			common.SysError("runtime drain incomplete; shared pools will not be closed under active work")
			return false
		}
	}

	// Client/root cancellation must not preempt the final metric flush.
	// Official gopool refunds have no custom durable drain/retry group.
	cleanup, finish := context.WithTimeout(context.Background(), 30*time.Second)
	defer finish()
	done := make(chan error, 1)
	go func() {
		if err := perfmetrics.Drain(cleanup); err != nil {
			done <- err
			return
		}
		model.FlushRuntimeData()
		done <- common.StopPyroScope()
	}()
	select {
	case err := <-done:
		if err != nil {
			common.SysError("runtime finalization failed: " + common.MaskSensitiveLogInfo(err.Error()))
			return false
		}
		return true
	case <-cleanup.Done():
		common.SysError("runtime finalization exceeded deadline; shared pools remain owned by unfinished work")
		return false
	}
}
