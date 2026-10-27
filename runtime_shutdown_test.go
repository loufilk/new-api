package main

import (
	"net/http"
	"testing"
	"testing/synctest"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/wsmanager"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShutdownWaitsForWebSocketClose(t *testing.T) {
	previousBatch, previousExport := common.BatchUpdateEnabled, common.DataExportEnabled
	common.BatchUpdateEnabled, common.DataExportEnabled = false, false
	t.Cleanup(func() {
		common.BatchUpdateEnabled, common.DataExportEnabled = previousBatch, previousExport
	})
	synctest.Test(t, func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		unregister := wsmanager.Register(1, wsmanager.KindResponses, func(string) {
			close(entered)
			<-release
		})
		defer unregister()
		app := newApplicationRuntime()
		done := make(chan bool, 1)
		go func() { done <- app.Shutdown(&http.Server{}) }()
		synctest.Wait()
		select {
		case <-entered:
		default:
			t.Error("shutdown did not start the WebSocket close callback")
		}
		select {
		case result := <-done:
			close(release)
			t.Fatalf("shutdown returned %v before the WebSocket close callback finished", result)
		default:
		}
		close(release)
		synctest.Wait()
		select {
		case result := <-done:
			assert.True(t, result, "completed WebSocket closure must allow graceful finalization")
		default:
			require.FailNow(t, "shutdown did not finish after the WebSocket close callback")
		}
	})
}
