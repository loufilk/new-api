package common

import (
	"runtime"
	"sync"

	"github.com/grafana/pyroscope-go"
)

var pyroMu sync.Mutex
var activeProfiler *pyroscope.Profiler

func StopPyroScope() error {
	pyroMu.Lock()
	profiler := activeProfiler
	activeProfiler = nil
	pyroMu.Unlock()
	if profiler != nil {
		return profiler.Stop()
	}
	return nil
}

func StartPyroScope() error {
	pyroMu.Lock()
	defer pyroMu.Unlock()
	if activeProfiler != nil {
		return nil
	}

	pyroscopeUrl := GetEnvOrDefaultString("PYROSCOPE_URL", "")
	if pyroscopeUrl == "" {
		return nil
	}

	pyroscopeAppName := GetEnvOrDefaultString("PYROSCOPE_APP_NAME", "new-api")
	pyroscopeBasicAuthUser := GetEnvOrDefaultString("PYROSCOPE_BASIC_AUTH_USER", "")
	pyroscopeBasicAuthPassword := GetEnvOrDefaultString("PYROSCOPE_BASIC_AUTH_PASSWORD", "")
	pyroscopeHostname := GetEnvOrDefaultString("HOSTNAME", "new-api")

	mutexRate := GetEnvOrDefault("PYROSCOPE_MUTEX_RATE", 5)
	blockRate := GetEnvOrDefault("PYROSCOPE_BLOCK_RATE", 5)

	runtime.SetMutexProfileFraction(mutexRate)
	runtime.SetBlockProfileRate(blockRate)

	profiler, err := pyroscope.Start(pyroscope.Config{
		ApplicationName: pyroscopeAppName,

		ServerAddress:     pyroscopeUrl,
		BasicAuthUser:     pyroscopeBasicAuthUser,
		BasicAuthPassword: pyroscopeBasicAuthPassword,

		Logger: nil,

		Tags: map[string]string{"hostname": pyroscopeHostname},

		ProfileTypes: []pyroscope.ProfileType{
			pyroscope.ProfileCPU,
			pyroscope.ProfileAllocObjects,
			pyroscope.ProfileAllocSpace,
			pyroscope.ProfileInuseObjects,
			pyroscope.ProfileInuseSpace,

			pyroscope.ProfileGoroutines,
			pyroscope.ProfileMutexCount,
			pyroscope.ProfileMutexDuration,
			pyroscope.ProfileBlockCount,
			pyroscope.ProfileBlockDuration,
		},
	})
	if err != nil {
		return err
	}
	activeProfiler = profiler
	return nil
}
