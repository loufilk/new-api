package payment

import "github.com/QuantumNous/new-api/pkg/paymentcore"

// PaymentOrderBinding aliases the generic host payment settlement snapshot so
// the extension ModelSet keeps registering the same
// chuanapi_payment_order_bindings table after the implementation moved to
// pkg/paymentcore (shared with the host provider handlers).
type PaymentOrderBinding = paymentcore.PaymentOrderBinding
