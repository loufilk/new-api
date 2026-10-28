-- ---------------------------------------------------------------------------
-- ChuanAPI / new-api rc.30 P0: 资金列统一升级为 64 位（MySQL >= 5.7.8）
--
-- 适用场景：rc.26 -> rc.30 升级前，或任何 users.quota_* 仍为 32 位 INT 的库。
--
-- 为什么必须做：
--   1. rc.30 启动前置检查 ensureUserQuotaColumns (model/main.go) 要求
--      users.quota / used_quota / aff_quota / aff_history 必须为 BIGINT，
--      否则拒绝启动。
--   2. 万川钱包的资金真源是 users.quota_paid / quota_bonus / quota_affiliate，
--      users.quota 只是三者合计的兼容镜像。启动迁移 migrateQuotaAccounts ->
--      BackfillLegacyQuotaAccounts 每次启动都会把 64 位 users.quota 回填进
--      quota_paid；若该列仍是 INT，在 MySQL 非严格模式下会**静默截断**为
--      2147483647，造成账实不符且无任何告警。
--
-- 执行方式：本脚本不自动提交，也不做任何破坏性降级。
--   步骤 0 / 1 为只读核对；步骤 2 为 ALTER；步骤 3a / 3b / 4 为升级后复核。
--   请逐段执行并保留输出作为审计证据。
--
-- 幂等性：所有 ALTER 均为「声明目标类型」，结果幂等——对已是 BIGINT 的列重复
--   执行不改变最终 schema，也不动数据。
--   但幂等只针对结果，不代表零开销：MySQL 不保证跳过「类型已相同」的 MODIFY，
--   重复执行可能仍触发全表重建。因此重跑前请先看步骤 3b 是否已返回 0 行，
--   已返回 0 行就无需再跑步骤 2。
--
-- 前置检查：本脚本假定这些表与列已存在（即库已被 rc.26 及以上版本初始化过）。
--   MySQL 没有 PostgreSQL 那样的 DO 块，缺表/缺列时 ALTER 会直接报错并中止该语句。
--   若你的库尚未创建 affiliate_rewards 等表，请先让应用完成一次 AutoMigrate，
--   或按步骤 0 的输出注释掉对应的 ALTER 行。
--
-- 回滚前提（降级回 rc.26 时才需要）：
--   仅当步骤 4e 的越界计数全部为 0（且 4d 的负值计数为 0）时，才可以把列
--   降级回 INT。本脚本**故意不提供**自动降级语句，避免误执行导致资金数据截断。
--   降级必须由人工确认越界计数后手写 ALTER，并在执行前完整备份。
-- ---------------------------------------------------------------------------

-- ===========================================================================
-- 步骤 0：修改前 schema 快照（只读）
-- 期望：本查询列出的每一行，DATA_TYPE 都应在步骤 3 变为 bigint。
-- ===========================================================================
SELECT TABLE_NAME,
       COLUMN_NAME,
       DATA_TYPE,
       COLUMN_TYPE,
       IS_NULLABLE,
       COLUMN_DEFAULT
FROM INFORMATION_SCHEMA.COLUMNS
WHERE TABLE_SCHEMA = DATABASE()
  AND (
        (TABLE_NAME = 'users' AND COLUMN_NAME IN
            ('quota', 'used_quota', 'aff_quota', 'aff_history',
             'quota_paid', 'quota_bonus', 'quota_affiliate'))
     OR (TABLE_NAME = 'balance_ledger_operations' AND COLUMN_NAME IN
            ('total_before', 'total_after'))
     OR (TABLE_NAME = 'balance_ledger' AND COLUMN_NAME IN
            ('delta', 'balance_before', 'balance_after', 'total_before', 'total_after'))
     OR (TABLE_NAME = 'affiliate_rewards' AND COLUMN_NAME IN
            ('eligible_paid_quota', 'reward_quota'))
      )
ORDER BY TABLE_NAME, COLUMN_NAME;

-- ===========================================================================
-- 步骤 1：修改前资金不变量核对（只读）
-- 三个查询都必须返回 0 行 / 0 计数；否则先查清账务问题再迁移。
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
-- 步骤 2：升级列类型为 BIGINT
-- 注意：MODIFY COLUMN 必须重述完整列定义，否则会丢失 NOT NULL / DEFAULT。
--       下列定义与 model/ 中的 GORM tag 一一对应。
-- ===========================================================================

-- users：官方四列（rc.30 启动检查要求）
ALTER TABLE users MODIFY COLUMN quota       BIGINT DEFAULT 0;
ALTER TABLE users MODIFY COLUMN used_quota  BIGINT DEFAULT 0;
ALTER TABLE users MODIFY COLUMN aff_quota   BIGINT DEFAULT 0;
ALTER TABLE users MODIFY COLUMN aff_history BIGINT DEFAULT 0;

-- users：万川钱包三列（资金真源，不可跳过检查）
ALTER TABLE users MODIFY COLUMN quota_paid      BIGINT DEFAULT 0;
ALTER TABLE users MODIFY COLUMN quota_bonus     BIGINT DEFAULT 0;
ALTER TABLE users MODIFY COLUMN quota_affiliate BIGINT DEFAULT 0;

-- balance_ledger_operations：幂等头的总额快照
ALTER TABLE balance_ledger_operations MODIFY COLUMN total_before BIGINT NOT NULL;
ALTER TABLE balance_ledger_operations MODIFY COLUMN total_after  BIGINT NOT NULL;

-- balance_ledger：账本明细（资金审计真源）
ALTER TABLE balance_ledger MODIFY COLUMN delta          BIGINT NOT NULL;
ALTER TABLE balance_ledger MODIFY COLUMN balance_before BIGINT NOT NULL;
ALTER TABLE balance_ledger MODIFY COLUMN balance_after  BIGINT NOT NULL;
ALTER TABLE balance_ledger MODIFY COLUMN total_before   BIGINT NOT NULL;
ALTER TABLE balance_ledger MODIFY COLUMN total_after    BIGINT NOT NULL;

-- affiliate_rewards：返利金额
ALTER TABLE affiliate_rewards MODIFY COLUMN eligible_paid_quota BIGINT NOT NULL;
ALTER TABLE affiliate_rewards MODIFY COLUMN reward_quota        BIGINT NOT NULL;

-- ===========================================================================
-- 步骤 3a：null / default 语义复核（只读）
-- MySQL 的 MODIFY COLUMN 会**丢弃所有未重述的列属性**。步骤 2 的重述与
-- model/ 中的 GORM tag 逐列一致（users 七列 nullable + DEFAULT 0；账本与返利
-- 九列 NOT NULL 无默认），但若存量库曾人工偏离过这份定义（例如给 users.quota
-- 加过 NOT NULL），MODIFY 会静默把它改回 nullable。
-- 做法：把本查询输出与步骤 0 的 IS_NULLABLE / COLUMN_DEFAULT 逐行对比。
-- 期望：除 DATA_TYPE 变为 bigint 外，两次输出完全一致。
--       任何 IS_NULLABLE / COLUMN_DEFAULT 差异都必须人工确认后再决定是否补回。
-- ===========================================================================
SELECT TABLE_NAME,
       COLUMN_NAME,
       DATA_TYPE,
       IS_NULLABLE,
       COLUMN_DEFAULT
FROM INFORMATION_SCHEMA.COLUMNS
WHERE TABLE_SCHEMA = DATABASE()
  AND (
        (TABLE_NAME = 'users' AND COLUMN_NAME IN
            ('quota', 'used_quota', 'aff_quota', 'aff_history',
             'quota_paid', 'quota_bonus', 'quota_affiliate'))
     OR (TABLE_NAME = 'balance_ledger_operations' AND COLUMN_NAME IN
            ('total_before', 'total_after'))
     OR (TABLE_NAME = 'balance_ledger' AND COLUMN_NAME IN
            ('delta', 'balance_before', 'balance_after', 'total_before', 'total_after'))
     OR (TABLE_NAME = 'affiliate_rewards' AND COLUMN_NAME IN
            ('eligible_paid_quota', 'reward_quota'))
      )
ORDER BY TABLE_NAME, COLUMN_NAME;

-- ===========================================================================
-- 步骤 3b：修改后 schema 复核（只读）
-- 期望：本查询返回 0 行。任何返回行都表示该列仍非 bigint，rc.30 会拒绝启动
--       （或钱包列会静默截断）。
-- ===========================================================================
SELECT TABLE_NAME, COLUMN_NAME, DATA_TYPE, COLUMN_TYPE
FROM INFORMATION_SCHEMA.COLUMNS
WHERE TABLE_SCHEMA = DATABASE()
  AND DATA_TYPE <> 'bigint'
  AND (
        (TABLE_NAME = 'users' AND COLUMN_NAME IN
            ('quota', 'used_quota', 'aff_quota', 'aff_history',
             'quota_paid', 'quota_bonus', 'quota_affiliate'))
     OR (TABLE_NAME = 'balance_ledger_operations' AND COLUMN_NAME IN
            ('total_before', 'total_after'))
     OR (TABLE_NAME = 'balance_ledger' AND COLUMN_NAME IN
            ('delta', 'balance_before', 'balance_after', 'total_before', 'total_after'))
     OR (TABLE_NAME = 'affiliate_rewards' AND COLUMN_NAME IN
            ('eligible_paid_quota', 'reward_quota'))
      )
ORDER BY TABLE_NAME, COLUMN_NAME;

-- ===========================================================================
-- 步骤 4：修改后资金不变量 + 越界核对（只读）
-- 4a/4b/4c 必须与步骤 1 结果一致（均为 0）。
-- 4d 复核零负余额不变量，它是 4e 只查上界的前提。
-- 4e 是「能否降级回 INT」的唯一判据：全为 0 才允许人工降级。
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

-- 4e. 32 位越界计数（降级前提；任何一项非 0 即禁止降级回 INT）
--     余额、合计、返利金额按上述不变量恒为非负，故只查上界 2147483647。
--     balance_ledger.delta 是**唯一带符号**的资金列（扣减时为负，
--     model/balance_ledger.go:566 `delta: -take`），因此必须查双边界。
--     不要用 ABS(delta)：delta = -9223372036854775808 时 ABS() 在 MySQL 下
--     溢出报错（BIGINT 上界比下界绝对值小 1），会让本查询失败而非返回计数。
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
