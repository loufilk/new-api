# 万川简约代理 A07：阶段汇报 A15

日期：2026-11-03。持续验收，不部署生产。

## 本阶段页面与资金实测

- 普通用户登录后没有代理导航和代理余额；钱包分别显示充值来源余额、赠金与普通邀请奖励，截图 `A15-ordinary-wallet.png`。
- 代理从总览生成 $1 直接码，余额从 $1,000 降至 $999；确认未交付后作废，记录变为禁用且完整码遮挡，余额恢复 $1,000。
- 生成面值 $1、售价 ¥1.20 的收款页，预览成本 ¥0.90、渠道手续费 ¥0.02、收益 $0.28；付款前客户页没有兑换码。向本机演示网关提交合成签名成功回调后，客户页交付唯一兑换码，截图 `A15-paid-delivery.png`。没有真实支付。
- root 默认看不到该码完整值；登记合成退款凭证并确认后，记录变为已退款、收益已取消，操作人与凭证可查询，截图 `A15-root-refund-result.png`。
- 钱包邀请返利划转 $1 到本人充值余额：余额 $200→$201，已解冻返利 $30→$29，总览显示同为 $29、累计仍为 $35。
- root 登记合成邀请提现 $20，历史显示手续费 $0.4、净额 $19.6、来源、操作人与凭证；邀请余额 $29→$9，另有 $5 冻结。截图 `A15-root-withdrawal-history.png`。演示记录保留供用户查看，不对应实际资金。

截图目录：`/Users/wusicheng/.codex/visualizations/2026/11/01/01a19563-8514-7171-86b5-334ce8a29bb3/`。

## 实测发现并修复

- root 原先显示代理销售三入口，点击个人总览没有自己的代理账户。现只保留“代理与提现”管理入口，root 打开 `/agent-platform` 跳转 `/agent-platform/finance?section=agents`。已在 Chrome 验证；普通代理保持三个入口。
- 作废成功后确认弹窗原先不关闭，现成功后立即关闭；按钮改为“作废兑换码”，未使用码状态使用“未使用”。新增关闭行为断言。
- 邀请划转初值原为 1 quota，显示极小数字且最小金额提示被格式化为另一数值。现默认填 $1（不足 $1 则填全部可用），保留后端最小 1 quota 规则，移除容易误解的提示。
- 钱包 PAT 路由测试补齐 A07 所需邀请/代理表，保留真实路由与中间件权限检查；本阶段已单独提交为 `c3e68455`。

## 验证证据与全仓边界

- 当前 A07 模型定向、控制器/路由定向通过：`/tmp/wanchuan-a07-current-model-a15.log`、`/tmp/wanchuan-a07-current-http-a15.log`。钱包包在该筛选无匹配项，不当作其整包通过。
- 钱包/作废/导航相关 4 文件 43 项通过，TypeScript 和 lint 通过：`/tmp/wanchuan-a07-final-front-a15.log`、`/tmp/wanchuan-a07-final-types-a15.log`、`/tmp/wanchuan-a07-final-lint-a15.log`。
- 前端与后端构建：`/tmp/wanchuan-a07-build-a15-final.log`、`/tmp/wanchuan-a07-backend-build-a15-final.log`，本地预览持续使用 3107 端口。
- 在独立 `04f958fb` 工作树复跑旧失败，14 个顶层失败在基线复现：`/tmp/wanchuan-a07-baseline-failures-a15.log`，包括支付夹具缺表、普通兑换码、relay、路由快照、i18n、authz。不能据此推断所有其他失败均为基线。
- 配置原生 PostgreSQL 后的全仓运行仍失败：`/tmp/wanchuan-a07-full-native-a15.log`。其中旧行锁测试污染全局方言，令后续 PG 查询误用反引号；另一个 channel fixture 改用 SQLite 却保留 PG 类型。现已修复两处测试状态恢复；同序定向 PG 与 SQLite 回归通过，日志 `/tmp/wanchuan-a07-dialect-leak-a15.log`、`/tmp/wanchuan-a07-final-dialect-a15.log`。
- 修复后的 PostgreSQL 模型整包重新运行中：`/tmp/wanchuan-a07-model-native-a16.log`。旧现金佣金测试与 A07 不同、其他 native fixture 问题仍需逐项区分，不能宣称全仓通过。
- GitHub 推送再次返回 403：`/tmp/wanchuan-a07-push-a15.log`，当前账号 `jasonhuang1236-commits` 无 `loufilk/new-api` 写权限。未读取/替换凭证，未创建 PR。

## 决定

页面与主要业务链路已完成本地实测；继续处理全仓原生测试的状态泄漏及与 A07 变更相关的旧断言，整理最终迁移前置条件和交付证据。远端推送仍需仓库写权限，不部署生产。
