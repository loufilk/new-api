package controller

import "github.com/QuantumNous/new-api/pkg/paymentcore"

func paymentReturnPath(suffix string) string {
	return paymentcore.ReturnPath(suffix)
}
