# USDT wallet recharge: staged rollout only

This separate rail is disabled by default. It changes no existing fiat,
WeChat, invoice, agent or Waffo flow. Manage it under System Settings →
Billing → Payment Gateway → USDT recharge. A secret or recovery phrase for a
receiving address never belongs in New API.

- Managed migration adds only `usdt_top_up_configs` and `usdt_top_up_orders`.
  No historical wallet or fiat orders are rebuilt. Keep these tables on
  rollback. Drain/reconcile admitted orders before rolling back to a binary
  without the USDT webhook.
- Orders and limits are in CNY. Each CNY of principal yields one internal wallet unit
  (`common.QuotaPerUnit` stored quota). The exact conversion is frozen in the
  order. Displayed units depend on site currency/quota settings; ensure staging
  labels match the deployed display configuration before enabling. Per-network
  bonus rounds down to a fen and posts to the restricted bonus balance.
  It does not change the charged amount. Fiat discounts, pricing groups,
  invite rebates, new-user promos, agent payout and invoice flow do not apply.
- Activation requires the current payment-compliance acknowledgement, a
  configured gateway, and at least one enabled network. Disabling blocks new
  admissions without preventing admitted orders from settling. Do not change
  provider origin, merchant ID or secret until open orders have been reconciled.
- Browser returns and public status checks cannot credit funds. A valid
  signed GMPay v2 callback must match the original network, merchant, trade
  ID, precise fiat and USDT amounts and address. Server-side detail and paid
  status must also match. No late payment or unclear order is credited
  automatically. A single transaction hash may credit only once per chain,
  because the upstream callback contains no transfer event index.
- Review expiry exceptions, wrong-network/amount evidence, and ambiguous
  create calls on the dedicated provider and ledger. Never send another order
  with the same idempotency key; replay only reads its recorded result.
  Never manually complete a USDT order via a fiat order operation.
- If checkout terms changed before admission, the server echoes an explicit
  rejection bound to the original request, network and amount. The wallet
  reloads the current options and asks the customer to review again.
  A lost response, an unrecognized error or a duplicate is **not** this signal.
  Check an uncertain request via the signed-in wallet (“Check order status”);
  this only reads the customer's receipt and does not create a provider order.
  A 404 does not prove that the original request will never commit. Keep the
  request reference for support, and do not send another payment. This lookup
  also works after the payment network is disabled and beyond the latest 50
  entries shown in history.

## Provider and staging

Pin [EPUSDT v2.1.5](https://github.com/GMWalletApp/epusdt/releases/tag/v2.1.5),
commit `56e2c2dfc09b8667d4a3c085ca542ee8c4bcfaa5`, and verify its image digest.
Use a dedicated merchant, native USDT only, with strict chain verification
and reviewed confirmation depths. New API is not an independent chain/RPC
verifier; it independently compares its frozen order with signed and status
responses from that pinned provider.

Set `USDT_GMPAY_ORIGIN`, `USDT_GMPAY_PID`, `USDT_GMPAY_SECRET`, and
`USDT_CALLBACK_ORIGIN` via the server's secret configuration. Only canonical
HTTPS origins are accepted. Set up the callback route
`POST /api/user/usdt/notify` at the callback origin. The secret must not be
stored in options, the database, the browser, or logs.
The Compose serving service passes these optional environment values through;
supply them through protected deployment configuration only. The managed
migrator does not need them, and managed startup does not load an application
`.env` file. Missing values leave this rail unavailable. Supplying them does
not enable payment: the admin switch is separate and defaults to off.

EPUSDT v2.1.5's callback requires publicly validated destination IPs and does
not follow redirects. A loopback TLS mock exercises New API only; it does not
prove provider-to-merchant delivery. Test the pinned image, public HTTPS,
proxy and callback retries on an isolated staging installation first.
The customer wallet shows the locally generated QR and lets the customer copy
the original address only after confirming the order. The hosted cashier URL
and provider trade ID stay internal and do not appear in customer responses.
Do not switch network, renew the quote, or submit after the local expiry: only
the original address, selected chain, exact amount and deadline apply. Do not
accept off-order transfers.

New API has no automatic payouts, wallet-key custody, or manual-credit button
for this rail. Review exceptions with the pinned provider’s console and New API
read-only financial records during staging. No dedicated operator reconciliation
queue is included.
