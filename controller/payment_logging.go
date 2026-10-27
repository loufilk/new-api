package controller

import "github.com/QuantumNous/new-api/pkg/paymentcore"

// paymentPayloadSummary gives operators a stable correlation value without
// persisting payment payloads, customer data, or webhook signatures in logs.
func paymentPayloadSummary(payload []byte) string {
	return paymentcore.PayloadSummary(payload)
}
