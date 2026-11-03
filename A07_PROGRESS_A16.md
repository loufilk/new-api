# 万川简约代理 A07：阶段汇报 A16

日期：2026-11-03。继续验收，不部署生产。

## 本阶段完成

- 将旧佣金模型测试改成明确的历史现金权益夹具：新版真实充值仍走 A07 邀请返利，不为让旧断言通过而恢复旧现金政策。保留历史退款、封顶记录、来源、并发重放、提现、权限降级和凭证恢复保护。
- controller 的旧佣金/活动/治理测试直接放入既有权益，继续验证原有历史处理与隐私控制，不假定 A07 新支付会创建旧账户。
- 修复退役 outbox 处理的错误残留：无 inbox 记录时，处理 cash/legacy 分支原先残留 `ErrRecordNotFound`，无法标记已处理。现在只确认退役事件，不再发新现金或邀请奖励；重复处理不重复生效。
- 迁移门槛增加未处理旧 cash/legacy 支付权益。即使余额暂时为 0，只要仍有待处理旧权益，也拒绝 A07 切换并要求人工核查，不静默清掉。
- 更新注册迁移用例：新库和重复迁移应成功；存在旧代理负债时应保留原资金和凭证，并明确拒绝标记 A07 已切换。
- 准备 `A07_RELEASE_CHECKLIST_A01.md`，包括生产只读 SQL、备份副本试迁移、守恒核对、真实支付配置和回滚边界。SQL 已在本机演示 SQLite 以只读连接逐条验证；未读取生产数据或凭证。

## 验证证据

- 最新三真实数据库矩阵 9/9 通过：`/tmp/wanchuan-a07-matrix-a16/results.json`，含新增退役事件与迁移阻断回归、全新/升级/重复迁移、独立日志库启动。
- PostgreSQL 历史模型/贡献/治理回归通过：`/tmp/wanchuan-a07-legacy-fixtures-a16-v2.log`；旧控制器相关回归通过：`/tmp/wanchuan-a07-controller-legacy-a16-v2.log`。
- 真正配置注册迁移专用 DSN 的模块整包通过：`/tmp/wanchuan-a07-registered-migration-a16.log`，本轮运行实际 PG fixture，没有以 skip 代替。
- PostgreSQL 邀请返利服务回归通过：`/tmp/wanchuan-a07-service-referral-a16.log`；架构边界通过：`/tmp/wanchuan-a07-architecture-a16.log`。
- 相关前端整组 285 项通过：`/tmp/wanchuan-a07-front-release-a16.log`。最终 TypeScript、lint、前端构建见 A15 最终日志；后端本轮构建 `/tmp/wanchuan-a07-backend-build-a16.log` 通过。本地服务已重启并 Ready。

## 全仓原生测试边界

A15 方言修复后、旧历史夹具调整前，PostgreSQL 模型整包 `/tmp/wanchuan-a07-model-native-a16.log` 剩 21 个顶层失败：15 个旧现金佣金前提失效，6 个在原始 `04f958fb` 的相同原生用例中复现。后者证据为 `/tmp/wanchuan-a07-baseline-native-a16.log`，分别涉及 bonus hold 唯一键、旧清理工具查询 SQLite 元表、会话刷新/撤销和用户并发更新/软删 token 测试。A07 专属模型测试在该整包运行中全部通过。

历史夹具调整后的模型整包已完成：`/tmp/wanchuan-a07-model-native-a16-final.log`，217.980 秒，剩余恰为上述 6 个原始基线可复现用例；没有 A07 或旧代理资金兼容用例失败。整包退出码仍为 1，不能称模型整包全绿。全项目其他已复现基线见 A15 报告；本版不扩展到与代理无关的网关、relay、i18n 和用户管理修复。

原生环境额外出现的性能桶、financial review 和 Midjourney hold 三个失败也已在 `04f958fb` 复现，见 `/tmp/wanchuan-a07-baseline-extra-a16.log`。它们与 A15 的 14 个基线失败和本阶段 6 个模型基线分别记录，不以缺环境代替检查。

## 决定

核心本地功能及业务链路已验证，继续整理最终整包测试和交付状态；GitHub 当前账号仍缺仓库写权限，不更换凭证、不创建 PR、不部署生产。
