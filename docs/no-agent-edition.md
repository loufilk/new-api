# 独立无代理版本 / Non-agent edition

## 基线与范围

本分支基于 `dot/sub2-tool-descriptions-20261101` 的提交
`04f958fb73fdb4944a0a1c925854a57a5d7b2f6a`，不是回退上游。
它移除代理系统的可执行实现，而非功能开关或隐藏导航。

移除：代理 Profile/授权与事务 proof、代理预算/批发兑换码、采购/交付、
现金佣金/债务/提现/人工打款、信用额度与 AR、押金/退款、代理活动、协议、
对账单、代理风控、代理后台调度、两组代理 HTTP 路由及前端页面。
`/api/agent-platform/*` 与 `/api/agent-redemption/*` 不再注册。
普通兑换码管理恢复为管理员权限，旧角色 5 无此权限。

保留：图片 HD/数量/结算修复、官方同步、用户级调用拦截和自定义回复日志、
默认关闭的工具描述兼容、普通注册/邀请返利、支付充值、三钱包、订阅、模型
API Key、管理 PAT、正常消费及账务恢复。普通后台任务仍运行。

历史 `users.role=5` 不改写；服务端有效权限和自助 DTO 只按普通用户处理。
账号、现存 API Key、余额、消费记录不会因代理资格被删改，也不会升级权限。
新增 role=5 和 `set_agent`/`unset_agent` 操作被拒绝。

### 不能一起删掉的共享财务安全

- 普通支付回执使用 `PaymentReceipt`，**沿用**物理表
  `agent_payment_receipts` 与原唯一索引，防止旧支付被跨订单或跨用途复用。
  新回执只允许普通 `topup` 用途。这不是代理业务接口。
- 普通邀请前三次充值的返利采用独立 `referral_reward_outboxes/inboxes`。
  本金和资格/比例/次数快照原子写入；邀请人异常不阻断已付本金；失败事件
  可重试，幂等收件记录防止重复奖励。旧代理 outbox 不被迁移或重放。
- BonusSource 来源、预扣、退款恢复、到期/撤销与对账继续保留，避免来源信息
  丢失或把未知交付直接退款。普通 `aff_quota` 划转保留原子账本和幂等键。
- 普通兑换码不能使用或编辑旧代理来源标记；已用码不能复活。创建支持
  `request_id` 幂等，管理员操作在事务内复核当前会话。

## 上线限制：不是自动清算或历史财务迁移器

**可直接验证的目标是新部署或没有未了代理财务的站点；已有代理资金历史
的生产站不承诺可直接切换。** 本轮不部署、不连生产数据库、不真实打款。

每个节点连接数据库后、任何启动迁移/后台调度前，运行只读
`NonAgentCutoverPreflight`：

- 未结预算/保留额度、现金佣金及债务、信用/核销风险、押金、在途付款退款、
  未完成订单/交付、未处理 outbox、未对账事项、未知状态或缺列/读取失败均阻断。
- 活动赠金有可用额度、已扣待恢复来源或来源守恒不完整也阻断；普通赠金来源不阻断。
- 余额为零不等于退休：未逆转的现金佣金仍可能被后续退款追偿。
- 批发资金来源批次/分片/经济分摊、信用 draw、非普通充值用途的支付回执等非空历史，若
  无法独立证明完整来源链已退休，本版保守阻断。需要另外设计并批准历史
  财务退休迁移；**不能删历史行或手工清零让检查通过**。
- 纯用户资格/普通邀请/配置/审计归档本身不阻断；普通充值回执可正常保留，
  新普通站再次启动不会因这张旧命名共享表而失败。
- 不 DROP 旧表，不重写历史金额/角色，不将核销当作债务清零，不提供忽略检查
  的环境开关。结果只包含表名/数量，不输出收款人、支付凭据或连接秘密。

检查不是并发切换协议。切换前必须停止全部旧版写入者和代理后台任务，
备份数据库和原运行版本，完成真实业务对账，再以只读副本预演。禁止旧版
代理节点与本版同时写同一个库。检查拒绝时保留原版处理，不强行替换。

### 回退

- **尚未接收新流量：**保留数据库和所有旧代理表，切回已备份的原版二进制/
  镜像与其配置；不需要反向删表。
- **已经接收新流量：**先停写并核对普通 `referral_reward_outboxes` 全部 `done`、
  消费结算/退款恢复无未了事项，再评估旧版兼容。旧版不认识新普通返利队列，
  未完成队列时不能直接回退或声称已结算。
- 不能把切换前数据库快照覆盖到已经接收付款/消费的新库；这会丢失新资金事实。
  不确定付款/退款状态继续保持原业务号和冻结状态，交由原版/人工对账。

## 验证与复现

使用声明的 Go 工具链和 Bun。仅对拥有的、合成数据 PostgreSQL **18.6**运行：

```sh
PG_BIN=/path/to/postgresql-18.6/bin \
NO_AGENT_EVIDENCE_DIR=/private/evidence \
  bin/test-no-agent-postgres.sh
```

脚本创建全新 loopback-only 集群，严格校验 `180006`，结束时停止自己的实例。
不接受生产 DSN，不运行 MySQL/SQLite，不调用付费上游。测试包含真实 PG 的
普通支付/返利/兑换/会话权限、只读迁移阻断、原用户拦截、工具描述 mock relay，
以及生产模型注册后的首次/重复启动迁移。

补充消费/退款矩阵（只选 PostgreSQL 子用例；独立日志库）：

```sh
PG_BIN=/path/to/postgresql-18.6/bin NO_AGENT_EVIDENCE_DIR=/private/consumption \
  bin/test-no-agent-postgres.sh bin/test-no-agent-consumption.sh
```

升级预演使用同一个自有集群包装器与 `bin/test-no-agent-upgrade.sh`，并设
`NO_AGENT_BASELINE_ROOT` 指向干净的上述固定祖先 checkout。它先构建原版完整
schema，加入合成普通余额/role 5/普通支付回执，启动本版两次逐字比对，再验证
真实初始化入口对未了财务拒绝启动。不得把该脚本改接生产库。

`bin/no-agent-fixture` 只用于自有合成 PG 的迁移验证，不启动 HTTP 或后台任务。
完整结果、额外消费恢复验证、前端测试/构建、已知未验证项见交付验收记录；
脚本存在不代表每一阶段已经通过。遵守本轮限制，不能宣称三数据库兼容验证。

## 安全参考

认证改动按 OWASP ASVS **5.0.0** 的认证/会话/授权边界复核：权限服务端决定、
敏感管理员操作保留当前会话与逐操作 proof、撤销会话不可重放、cookie 不能
替代浏览器 Authorization、审计不记录凭据。
参考 [Authentication Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html)、
[Session Management Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)
及 [ASVS 稳定版本](https://asvs.owasp.org/)。这是相关控制复核，不是全站 ASVS 认证。
