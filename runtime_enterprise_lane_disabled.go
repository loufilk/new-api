//go:build !enterprise_lane_lab

package main

import (
	"context"

	"github.com/QuantumNous/new-api/pkg/lifecycle"
)

func startEnterpriseLaneProjection(*lifecycle.Group)      {}
func drainEnterpriseLaneProjection(context.Context) error { return nil }
