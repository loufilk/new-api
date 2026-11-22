-- ---------------------------------------------------------------------------
-- ChuanAPI / new-api rc.30 P0: 资金列统一升级为 64 位（PostgreSQL >= 9.6）
--
-- 适用场景：rc.26 -> rc.30 升级前，或任何 users.quota_* 仍为 32 位 integer 的库。
--
-- 为什么必须做：
--   1. rc.30 启动前置检查 ensureUserQuotaColumns (model/main.go) 要求
--      users.quota / used_quota / aff_quota / aff_history 必须为 bigint/int8，
--      否则拒绝启动。
--   2. 万川钱包的资金真源是 users.quota_paid / quota_bonus / quota_affiliate，
--      users.quota 只是三者合计的兼容镜像。启动迁移 migrateQuotaAccounts ->
--      BackfillLegacyQuotaAccounts 每次启动都会把 64 位 users.quota 回填进
--      quota_paid。PostgreSQL 在溢出时会直接报错（不像 MySQL 非严格模式静默截断），
--      因此症状是启动迁移失败——同样必须先升级列。
--
-- 执行方式：本脚本不自动提交，也不做任何破坏性降级。
--   步骤 0 / 1 为只读核对；步骤 2 为 ALTER；步骤 3a / 3b / 4 为升级后复核。
--   请逐段执行并保留输出作为审计证据。
--
-- 幂等性：步骤 2 用 DO 块先判类型，已是 bigint 的列会被跳过（同时避免无谓的
--   全表重写）。重复执行无副作用。
--
-- 运维注意：int -> bigint 在 PostgreSQL < 17 需要全表重写，并持有
--   ACCESS EXCLUSIVE 锁。请在维护窗口执行，并预估 users / balance_ledger 的体量。
--
-- 回滚前提（降级回 rc.26 时才需要）：
--   仅当步骤 4e 的越界计数全部为 0（且 4d 的负值计数为 0）时，才可以把列
--   降级回 integer。本脚本**故意不提供**自动降级语句，避免误执行导致资金数据
--   溢出报错或丢失。降级必须由人工确认越界计数后手写 ALTER，并在执行前完整备份。
-- ---------------------------------------------------------------------------

-- ===========================================================================
-- 步骤 0：修改前 schema 快照（只读）
-- 期望：本查询列出的每一行，data_type 都应在步骤 3 变为 bigint。
-- ===========================================================================
SELECT table_name,
       column_name,
       data_type,
       is_nullable,
       column_default
FROM information_schema.columns
WHERE table_schema = current_schema()
  AND (
        (table_name = 'users' AND column_name IN
            ('quota', 'used_quota', 'aff_quota', 'aff_history',
             'quota_paid', 'quota_bonus', 'quota_affiliate'))
     OR (table_name = 'balance_ledger_operations' AND column_name IN
            ('total_before', 'total_after'))
     OR (table_name = 'balance_ledger' AND column_name IN
            ('delta', 'balance_before', 'balance_after', 'total_before', 'total_after'))
     OR (table_name = 'affiliate_rewards' AND column_name IN
            ('eligible_paid_quota', 'reward_quota'))
      )
ORDER BY table_name, column_name;

-- ===========================================================================
-- 步骤 1：修改前资金不变量核对（只读）
-- 三个查询都必须返回 0；否则先查清账务问题再迁移。
-- ===========================================================================

-- 1a. 镜像不变量：quota 必须等于三账户合计
SELECT COUNT(*) AS quota_mirror_mismatch_count
FROM users
WHERE quota <> quota_paid + quota_bonus + quota_affiliate;

-- 1b. 明细行必须都挂在存在的 operation 上（孤儿行）
SELECT COUNT(*) AS orphan_ledger_row_count
FROM balance_ledger l
         LEFT JOIN balance_ledger_operations o ON o.id = l.operation_id
WHERE o.id IS NULL;

-- 1c. 同一 operation 内明细 delta 合计必须等于 operation 的总额变动
SELECT COUNT(*) AS operation_delta_mismatch_count
FROM (SELECT o.id
      FROM balance_ledger_operations o
               JOIN balance_ledger l ON l.operation_id = o.id
      GROUP BY o.id, o.total_before, o.total_after
      HAVING SUM(l.delta) <> o.total_after - o.total_before) AS mismatched;

-- ===========================================================================
-- 步骤 2：升级列类型为 bigint（幂等：已是 bigint 则跳过，不触发全表重写）
-- PostgreSQL 的 ALTER COLUMN ... TYPE 会保留 NOT NULL 与 DEFAULT，
-- 因此无需像 MySQL 那样重述完整列定义（DEFAULT 0 会被隐式转为 bigint 的 0）。
--
-- 缺表 / 缺列：先查 information_schema.columns，不存在则 RAISE NOTICE 后跳过，
--   不报错。这让脚本能安全地跑在尚未创建 affiliate_rewards 等表的旧库上。
--   注意 information_schema.columns 只列出当前角色有权限的列，请用表 owner 执行，
--   否则「无权限」会被误判为「列不存在」而静默跳过。
--
-- 原子性与锁：DO 块是**单条语句**，因此整段要么全部生效要么全部回滚；
--   代价是它会同时持有 users / balance_ledger_operations / balance_ledger /
--   affiliate_rewards 四张表的 ACCESS EXCLUSIVE 锁直到全部完成。
--   若某张表体量大到无法在维护窗口内完成，请把 VALUES 列表拆成多次执行，
--   而不是让整段长时间阻塞全部资金表。
--
-- 依赖对象：若有视图、生成列或外部工具的物化视图引用这些列，ALTER 会报错中止
--   （整个 DO 块回滚）。请先用 pg_depend 排查依赖再执行。
-- ===========================================================================
DO $$
DECLARE
    target      RECORD;
    altered     INT := 0;
    skipped     INT := 0;
BEGIN
    FOR target IN
        SELECT t.table_name, t.column_name
        FROM (VALUES
                  ('users', 'quota'),
                  ('users', 'used_quota'),
                  ('users', 'aff_quota'),
                  ('users', 'aff_history'),
                  ('users', 'quota_paid'),
                  ('users', 'quota_bonus'),
                  ('users', 'quota_affiliate'),
                  ('balance_ledger_operations', 'total_before'),
                  ('balance_ledger_operations', 'total_after'),
                  ('balance_ledger', 'delta'),
                  ('balance_ledger', 'balance_before'),
                  ('balance_ledger', 'balance_after'),
                  ('balance_ledger', 'total_before'),
                  ('balance_ledger', 'total_after'),
                  ('affiliate_rewards', 'eligible_paid_quota'),
                  ('affiliate_rewards', 'reward_quota')
             ) AS t(table_name, column_name)
    LOOP
        -- 表或列不存在时跳过（例如尚未创建返利表的旧库）
        IF NOT EXISTS (SELECT 1
                       FROM information_schema.columns c
                       WHERE c.table_schema = current_schema()
                         AND c.table_name = target.table_name
                         AND c.column_name = target.column_name) THEN
            RAISE NOTICE 'skip %.% (column not present)', target.table_name, target.column_name;
            CONTINUE;
        END IF;

        IF EXISTS (SELECT 1
                   FROM information_schema.columns c
                   WHERE c.table_schema = current_schema()
                     AND c.table_name = target.table_name
                     AND c.column_name = target.column_name
                     AND c.data_type = 'bigint') THEN
            skipped := skipped + 1;
            CONTINUE;
        END IF;

        EXECUTE format('ALTER TABLE %I ALTER COLUMN %I TYPE bigint',
                       target.table_name, target.column_name);
        altered := altered + 1;
        RAISE NOTICE 'altered %.% to bigint', target.table_name, target.column_name;
    END LOOP;

    RAISE NOTICE 'quota bigint migration done: % altered, % already bigint', altered, skipped;
END
$$;

-- ===========================================================================
-- 步骤 3a：null / default 语义复核（只读）
-- PostgreSQL 的 ALTER COLUMN ... TYPE 会保留 NOT NULL 与 DEFAULT，但 DEFAULT
-- 表达式会被重铸为新类型（integer 的 0 变成 bigint 的 0）。
-- 做法：把本查询输出与步骤 0 的 is_nullable / column_default 逐行对比。
-- 期望：除 data_type 变为 bigint 外，两次输出语义一致。
--       column_default 允许出现 '0'::bigint 这类形式差异；
--       is_nullable 出现任何差异都属异常，必须人工确认。
-- ===========================================================================
SELECT table_name,
       column_name,
       data_type,
       is_nullable,
       column_default
FROM information_schema.columns
WHERE table_schema = current_schema()
  AND (
        (table_name = 'users' AND column_name IN
            ('quota', 'used_quota', 'aff_quota', 'aff_history',
             'quota_paid', 'quota_bonus', 'quota_affiliate'))
     OR (table_name = 'balance_ledger_operations' AND column_name IN
            ('total_before', 'total_after'))
     OR (table_name = 'balance_ledger' AND column_name IN
            ('delta', 'balance_before', 'balance_after', 'total_before', 'total_after'))
     OR (table_name = 'affiliate_rewards' AND column_name IN
            ('eligible_paid_quota', 'reward_quota'))
      )
ORDER BY table_name, column_name;

-- ===========================================================================
-- 步骤 3b：修改后 schema 复核（只读）
-- 期望：本查询返回 0 行。任何返回行都表示该列仍非 bigint，rc.30 会拒绝启动
--       （或启动迁移回填会溢出报错）。
-- ===========================================================================
SELECT table_name, column_name, data_type
FROM information_schema.columns
WHERE table_schema = current_schema()
  AND data_type <> 'bigint'
  AND (
        (table_name = 'users' AND column_name IN
            ('quota', 'used_quota', 'aff_quota', 'aff_history',
             'quota_paid', 'quota_bonus', 'quota_affiliate'))
     OR (table_name = 'balance_ledger_operations' AND column_name IN
            ('total_before', 'total_after'))
     OR (table_name = 'balance_ledger' AND column_name IN
            ('delta', 'balance_before', 'balance_after', 'total_before', 'total_after'))
     OR (table_name = 'affiliate_rewards' AND column_name IN
            ('eligible_paid_quota', 'reward_quota'))
      )
ORDER BY table_name, column_name;

-- ===========================================================================
-- 步骤 4：修改后资金不变量 + 越界核对（只读）
-- 4a/4b/4c 必须与步骤 1 结果一致（均为 0）。
-- 4d 复核零负余额不变量，它是 4e 只查上界的前提。
-- 4e 是「能否降级回 integer」的唯一判据：全为 0 才允许人工降级。
-- ===========================================================================

-- 4a. 镜像不变量
SELECT COUNT(*) AS quota_mirror_mismatch_count
FROM users
WHERE quota <> quota_paid + quota_bonus + quota_affiliate;

-- 4b. 孤儿明细行
SELECT COUNT(*) AS orphan_ledger_row_count
FROM balance_ledger l
         LEFT JOIN balance_ledger_operations o ON o.id = l.operation_id
WHERE o.id IS NULL;

-- 4c. operation 与明细合计一致
SELECT COUNT(*) AS operation_delta_mismatch_count
FROM (SELECT o.id
      FROM balance_ledger_operations o
               JOIN balance_ledger l ON l.operation_id = o.id
      GROUP BY o.id, o.total_before, o.total_after
      HAVING SUM(l.delta) <> o.total_after - o.total_before) AS mismatched;

-- 4d. 零负余额不变量：余额与合计列必须全部 >= 0
--     代码依据：model/balance_ledger.go:548-573 planDebit 以 take = balance 为上限，
--     不足时返回 ErrLedgerInsufficientBalance（:570），因此余额列不会被扣成负数。
--     本查询是该不变量的运行时复核；返回非 0 说明存量数据已违背不变量，
--     此时 4e 只查上界不再充分，必须先修复账务再评估降级。
SELECT
    (SELECT COUNT(*) FROM users
      WHERE quota < 0 OR used_quota < 0 OR aff_quota < 0 OR aff_history < 0
         OR quota_paid < 0 OR quota_bonus < 0 OR quota_affiliate < 0) AS users_negative,
    (SELECT COUNT(*) FROM balance_ledger_operations
      WHERE total_before < 0 OR total_after < 0)                      AS operations_negative,
    (SELECT COUNT(*) FROM balance_ledger
      WHERE balance_before < 0 OR balance_after < 0
         OR total_before < 0 OR total_after < 0)                      AS ledger_negative,
    (SELECT COUNT(*) FROM affiliate_rewards
      WHERE eligible_paid_quota < 0 OR reward_quota < 0)              AS rewards_negative;

-- 4e. 32 位越界计数（降级前提；任何一项非 0 即禁止降级回 integer）
--     余额、合计、返利金额按上述不变量恒为非负，故只查上界 2147483647。
--     balance_ledger.delta 是**唯一带符号**的资金列（扣减时为负，
--     model/balance_ledger.go:566 `delta: -take`），因此必须查双边界。
--     不要用 ABS(delta)：delta = -9223372036854775808 时 abs() 在 PostgreSQL 下
--     抛 bigint out of range（BIGINT 上界比下界绝对值小 1），会让本查询失败
--     而非返回计数。
SELECT
    (SELECT COUNT(*) FROM users
      WHERE quota > 2147483647 OR used_quota > 2147483647
         OR aff_quota > 2147483647 OR aff_history > 2147483647
         OR quota_paid > 2147483647 OR quota_bonus > 2147483647
         OR quota_affiliate > 2147483647)                        AS users_over_int32,
    (SELECT COUNT(*) FROM balance_ledger_operations
      WHERE total_before > 2147483647 OR total_after > 2147483647) AS operations_over_int32,
    (SELECT COUNT(*) FROM balance_ledger
      WHERE delta > 2147483647 OR delta < -2147483648
         OR balance_before > 2147483647
         OR balance_after > 2147483647 OR total_before > 2147483647
         OR total_after > 2147483647)                            AS ledger_over_int32,
    (SELECT COUNT(*) FROM affiliate_rewards
      WHERE eligible_paid_quota > 2147483647 OR reward_quota > 2147483647) AS rewards_over_int32;
