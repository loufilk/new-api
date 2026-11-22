# 万川简约代理 A07：阶段汇报 A08

日期：2026-11-03。本阶段完成本地预览更新和邀请奖励回归；整体上线验收继续进行。

## 已完成

- 前后端重新构建并重启独立预览，地址为 http://127.0.0.1:3107/agent-platform 。Chrome 已重新加载，确认三入口、代理余额、邀请奖励、最近记录和操作按钮正常展示。
- 邀请返利服务回归按 A07 更新为普通用户 3%、有效代理 5%、不限次数和冻结 5 天，保留本金不回滚、支付快照、幂等、并发、溢出、历史重放和退款隔离检查。
- 实际注册控制器回归覆盖密码注册、邮件重试、OAuth/LinuxDO、新客赠金只发一次、普通邀请不发及伪造字段不生效。
- 钱包增加代理邀请测试赠金的本人状态提示；不返回邀请人身份。已发放提示计入赠金，注册时未满足支付合规条件的记录提示联系客服。
- 钱包划转回归改为 A07 的普通会话和稳定幂等键，覆盖丢失响应重试、关闭重开同额重试、重复点击、余额边界和刷新；奖励说明与明细默认折叠。

## 本地查看

- 登录：http://127.0.0.1:3107/sign-in
- 代理总览：http://127.0.0.1:3107/agent-platform
- 兑换码中心：http://127.0.0.1:3107/redemption-codes?view=center
- 兑换码记录：http://127.0.0.1:3107/redemption-codes?view=records
- 钱包：http://127.0.0.1:3107/wallet
- root 管理：http://127.0.0.1:3107/agent-platform/finance

账号为 `a07_andy`、`a07_root`、`a07_user`，本地合成密码在 `~/Library/Application Support/WanchuanA07Preview/ACCOUNTS.txt`，不纳入 Git。预览只监听本机，使用独立 SQLite 和不可收款的演示网关，不连接生产数据。

截图：`/Users/wusicheng/.codex/visualizations/2026/11/01/01a19563-8514-7171-86b5-334ce8a29bb3/A08-local-agent-overview.png`。

## 验证证据

- `service` 整包通过：`/tmp/wanchuan-a07-service-a08.log`。
- 邀请返利 SQLite/真实 PostgreSQL 定向回归通过：`/tmp/wanchuan-a07-referral-native-a08-final.log`。
- 注册邀请控制器回归通过：`/tmp/wanchuan-a07-invitation-http-a08-final.log`。
- 钱包 SQLite 整包与真实 PostgreSQL 整包分别通过：`/tmp/wanchuan-a07-wallet-a08.log`、`/tmp/wanchuan-a07-wallet-native-a08-final.log`。后者使用 fixture 实际读取的 `TEST_AGENT_POSTGRES_DSN`，不是此前误用的变量。
- 前端相关 40 个文件共 245 项分批回归通过：广范围初次运行 244 项通过，唯一失败为旧组件名称的静态断言；更新为共享邀请卡后该文件 4 项全部通过。日志为 `/tmp/wanchuan-a07-front-wide-a08.log` 和 `/tmp/wanchuan-a07-contract-a08-final.log`。
- 涉及文件 TypeScript、lint、前端生产构建和 Go 二进制构建通过；日志 `/tmp/wanchuan-a07-types-a08-funding.log`、`/tmp/wanchuan-a07-lint-a08.log`、`/tmp/wanchuan-a07-build-a08.log`、`/tmp/wanchuan-a07-backend-build-a08.log`。
- 全仓 `go test ./...` 仍失败：日志 `/tmp/wanchuan-a07-full-go-a08.log`。包含未配置 opt-in PostgreSQL 的旧测试、支付 fixture、兑换码、relay、router、authz 等失败，尚未全部逐项基线对照，不能宣称均与本次改动无关。

## 后续必须完成

- 静态复核发现不足人民币一分的授信尾款可能无法在线结清，root 页面输入精度也需一起处理；需要实际回归后修复。
- 旧平台兑换码操作 Prepare 的更新及显式删除候选查询仍需补简约码隔离，防止读取代理码元数据；实际执行层已拒绝操作，但预览层仍须收口。
- 完整 A07 逐项验收、最后数据库矩阵和迁移前置条件仍待完成。本地预览用于看页面和测试流程，不代表已经达到上线验收标准。
- GitHub 推送此前因当前账户无写权限返回 403，本阶段没有变更凭证或声称远端更新成功。

## 决定

提供当前本地预览供用户查看；保留整体开发目标，继续修复已定位的边界问题和完成最终验收，不部署生产。
