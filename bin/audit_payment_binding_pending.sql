-- Read-only pre-deployment inventory for payment orders that predate
-- chuanapi_payment_order_bindings. This query is intentionally portable across
-- SQLite, MySQL and PostgreSQL and must not be changed into an automatic repair.

SELECT
  'topup' AS order_kind,
  t.id AS order_id,
  t.user_id,
  t.trade_no,
  t.payment_provider,
  t.payment_method,
  t.status,
  t.create_time
FROM top_ups AS t
LEFT JOIN chuanapi_payment_order_bindings AS b
  ON b.trade_no = t.trade_no
WHERE t.status = 'pending'
  AND t.payment_provider IN ('waffo', 'waffo_pancake')
  AND b.trade_no IS NULL

UNION ALL

SELECT
  'subscription' AS order_kind,
  s.id AS order_id,
  s.user_id,
  s.trade_no,
  s.payment_provider,
  s.payment_method,
  s.status,
  s.create_time
FROM subscription_orders AS s
LEFT JOIN chuanapi_payment_order_bindings AS b
  ON b.trade_no = s.trade_no
WHERE s.status = 'pending'
  AND s.payment_provider IN ('waffo', 'waffo_pancake')
  AND b.trade_no IS NULL

ORDER BY create_time, order_kind, order_id;
