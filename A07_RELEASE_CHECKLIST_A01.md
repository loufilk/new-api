# 万川简约代理 A07：上线前核查清单 A01

日期：2026-11-03。功能和金额规则来源：用户已授权实施的《万川代理系统简约方案 A07》（2026-11-01，2026-11-02 更新）。本文件只准备上线核查，不授权部署或读取生产凭证。

## 本地交付

- 活动仓库：`/Users/wusicheng/workspace-dev/new-api-agent-simplify`，分支 `bench`。
- 本地预览：http://127.0.0.1:3107 ，独立 SQLite、三类合成账号和不可收款支付网关；账号密码不入 Git。
- 本地阶段验收见 `A07_ACCEPTANCE_A02.md`，最新资金与迁移检查见 `A07_PROGRESS_A16.md`。
- 实际推送失败证据：`/tmp/wanchuan-a07-push-a15.log`，GitHub 返回当前 `jasonhuang1236-commits` 对 `loufilk/new-api` 没有写权限。获得该仓库写权限后，只推 `bench` 并回读完整提交哈希；不创建 PR、不触碰生产。

仓库所有者可在 https://github.com/loufilk/new-api/settings/access 添加协作者 `jasonhuang1236-commits` 并授予 Write 权限；该账号接受邀请后再重试推送。无需把密码或 token 发到聊天。授权完成后告诉助手“仓库写权限已开通”即可继续远端交付。

## 生产只读核查

由获授权的运维人员使用现有只读数据库连接，在停写前后核对。不要直接用 `bench` 程序连接生产来“试启动”：该程序的启动数据步骤包含 A07 收益迁移。

先确认实际数据库引擎、版本、表是否存在、备份完成时间及可恢复性，再执行下列只读查询。缺表时应记录为缺表并判断版本，不用创建表来绕过预检。

```sql
SELECT COALESCE(SUM(CASE WHEN aff_quota <> 0 THEN 1 ELSE 0 END), 0) AS users_with_old_referral,
       COALESCE(SUM(aff_quota), 0) AS old_referral_total,
       COALESCE(SUM(CASE WHEN aff_quota < 0 THEN 1 ELSE 0 END), 0) AS negative_referral_users
FROM users;

SELECT COUNT(*) AS old_budget_rows
FROM agent_redemption_accounts
WHERE allocated_quota <> 0 OR available_quota <> 0
   OR reserved_quota <> 0 OR spent_quota <> 0;

SELECT COUNT(*) AS old_fund_batches
FROM agent_fund_batches WHERE original_quota <> 0;

SELECT COUNT(*) AS old_commission_accounts
FROM agent_commission_accounts
WHERE earned_minor <> 0 OR debt_minor <> 0 OR held_minor <> 0;

SELECT COUNT(*) AS unsettled_old_entitlements
FROM agent_commission_outboxes
WHERE kind = 'payment' AND route IN ('legacy', 'cash') AND status <> 'done';

SELECT COUNT(*) AS a07_financial_events FROM agent_simple_ledgers;
SELECT id, transferred, credited, user_count, completed_at
FROM agent_simple_migrations;
```

同时核对每名用户迁入后的充值余额是否超出系统上限 `9,007,199,254,740,991 quota`。金额按现有站内额度单位核对，不当作人民币汇率。

发现旧代理非零资金、负邀请余额、未处理旧佣金事件、此前已产生的 A07 资金流水或任何无法解释的数据时，保存只读结果，先人工处理迁移方案；不得删流水或修改余额使检查“通过”。此前用户说明“没有代理资金记录”仍须以上线前的实际只读结果为准。

## 获得上线授权后才执行

1. 备份并验证恢复，停止旧程序和支付回调写入，保存切换时间与待处理订单清单。
2. 针对拟上线提交和实际引擎先在备份副本重复跑迁移；确认旧待使用收益划入充值余额、邀请可提现从 0 起算，赠金和其他余额不受影响。
3. 检查迁移记录的迁出与迁入总额相等，逐用户或按账本批次核对充值余额增量；重复启动不得再次划转。
4. 复核真实支付域名、回调与签名配置；确认合规设置和真实支付渠道成本，不把本机 `payment.example.test` 演示配置带上生产。
5. 小额验证普通充值返利、代理充值、收款码付款交付/退款、一次提现登记和权限隔离，再开放熟人试用。
6. 发生迁移失败或账额不符时保持停写并保全证据；按备份和支付回调清单恢复，不盲目回滚到只认识旧账本的程序继续收款。

## 决定

先完成本地代码与验收交付、解决 GitHub 写权限；生产核查和上线由用户另行授权，当前不执行。
