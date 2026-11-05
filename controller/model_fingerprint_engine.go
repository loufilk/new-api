package controller

import "github.com/QuantumNous/new-api/pkg/modelfingerprint"

// Keep the scoring boundary explicit so controller tests can verify admission,
// routing and billing without running the separately deployed scoring process.
// Production always uses the bounded local-socket client in modelfingerprint.
var modelFingerprintScorer = struct {
	ready      func() error
	challenges func() ([]modelfingerprint.Challenge, error)
	analyze    func([]modelfingerprint.Sample, bool) (modelfingerprint.Result, error)
}{modelfingerprint.Ready, modelfingerprint.GenerateChallenges, modelfingerprint.Analyze}
