#!/usr/bin/env bash
# Invoked ONLY inside test-no-agent-postgres.sh's owned disposable cluster.
set -euo pipefail
[[ ${NO_AGENT_FIXTURE_OWNED:-} == 1 && ${PGHOST:-} == 127.0.0.1 ]] || exit 2
root=$(cd "$(dirname "$0")/.." && pwd)
baseline=${NO_AGENT_BASELINE_ROOT:?Set the unchanged tool-description baseline checkout}
[[ $(git -C "$baseline" rev-parse HEAD) == 04f958fb73fdb4944a0a1c925854a57a5d7b2f6a ]] || exit 2
[[ -z $(git -C "$baseline" status --porcelain --untracked-files=no) ]] || exit 2
evidence=${NO_AGENT_EVIDENCE_DIR:?Set an evidence directory}
export SQL_DSN="postgres://postgres@127.0.0.1:$PGPORT/no_agent_upgrade?sslmode=disable"
(cd "$baseline" && go run "$root/bin/no-agent-fixture/main.go") > "$evidence/upgrade-baseline.log" 2>&1
"$PG_BIN/psql" -v ON_ERROR_STOP=1 -d no_agent_upgrade > "$evidence/upgrade-seed.log" <<'SQL'
INSERT INTO users (id,username,password,role,status,quota,quota_paid,quota_bonus,quota_reward,used_quota,request_count,aff_code,aff_count,aff_quota,aff_history,inviter_id,auth_version)
VALUES (97001,'fixture-consumer','synthetic-only-no-login',1,1,1200,1000,150,50,70,2,'fixture-one',3,25,75,0,4),
       (97002,'fixture-legacy','synthetic-only-no-login',5,1,2100,1800,200,100,80,4,'fixture-two',1,35,85,97001,6);
INSERT INTO agent_profiles(user_id,status,sales_frozen,settlement_frozen,version,updated_by,reason)
VALUES(97002,'active',false,false,1,97001,'synthetic identity only; no financial agent history');
INSERT INTO agent_payment_receipts (receipt_key,provider,provider_reference,merchant_scope,environment,purpose,local_order_key,owner_user_id,currency,currency_exponent,paid_minor,paid_at,verified_at)
VALUES(repeat('a',64),'stripe','pi_synthetic_ordinary_upgrade','synthetic-merchant','test','topup','synthetic-upgrade-order',97001,'USD',2,100,1000,1000);
SQL
snapshot() {
  "$PG_BIN/psql" -v ON_ERROR_STOP=1 -d no_agent_upgrade -At <<'SQL'
SELECT 'users:' || row_to_json(u)::text FROM (SELECT * FROM users ORDER BY id) u;
SELECT 'profiles:' || row_to_json(p)::text FROM (SELECT * FROM agent_profiles ORDER BY user_id) p;
SELECT 'receipts:' || row_to_json(p)::text FROM (SELECT * FROM agent_payment_receipts ORDER BY id) p;
SELECT 'ledger:' || row_to_json(l)::text FROM (SELECT * FROM balance_ledger ORDER BY id) l;
SELECT 'topups:' || row_to_json(t)::text FROM (SELECT * FROM top_ups ORDER BY id) t;
SQL
}
snapshot > "$evidence/upgrade-finances-before.txt"
(cd "$root" && go run ./bin/no-agent-fixture) > "$evidence/upgrade-first.log" 2>&1
snapshot > "$evidence/upgrade-finances-first.txt"
cmp "$evidence/upgrade-finances-before.txt" "$evidence/upgrade-finances-first.txt"
(cd "$root" && go run ./bin/no-agent-fixture) > "$evidence/upgrade-repeat.log" 2>&1
snapshot > "$evidence/upgrade-finances-repeat.txt"
cmp "$evidence/upgrade-finances-before.txt" "$evidence/upgrade-finances-repeat.txt"
# The same migration gate must refuse a genuine obligation and leave it intact.
"$PG_BIN/psql" -v ON_ERROR_STOP=1 -d no_agent_upgrade -c 'CREATE TABLE agent_unknown_fixture_liabilities (id bigint, amount_minor bigint); INSERT INTO agent_unknown_fixture_liabilities VALUES (1,100)' > "$evidence/upgrade-liability-seed.log"
if (cd "$root" && go run ./bin/no-agent-fixture) > "$evidence/upgrade-blocked.log" 2>&1; then
  echo 'ERROR: startup accepted an unknown financial obligation' >&2; exit 1
fi
grep 'non-agent cutover blocked' "$evidence/upgrade-blocked.log"
"$PG_BIN/psql" -v ON_ERROR_STOP=1 -d no_agent_upgrade -Atc 'SELECT amount_minor FROM agent_unknown_fixture_liabilities WHERE id=1' | grep '^100$'
printf 'Baseline upgrade repeated; all synthetic finance/identity snapshots unchanged; unknown liability blocked\n'
