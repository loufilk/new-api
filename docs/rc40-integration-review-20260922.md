# rc40 完整整合与旧定制复核记录

更新：2026-09-24。**第一步的已知源码修复及七个精选上游修复已纳入本提交；最终 SHA 的全量验收尚未通过，不得据此合并 main 或部署。**

## 基准与范围

- 仓库：1057300248/wanchuan-new-api。
- 分支：codex/rc40-full-integration-20260922。
- main 基准：7e8c2e633d18c15c1ef319728345363b1c3f9028。
- 本轮承接的定制提交：6691f29bd2feb3c89f9be396bfda169218f38176。
- 本轮直接父提交：0da705d207109be84d7252f92317772fd118b62c，仅增加过源码检查工作流；该临时工作流现已删除。
- 官方 rc40：0aec08fee811ec6136828fda790551b49e410301。
- 完整 rc40 合并提交：a1ee61c51d727f74880d5570628e65f0c9b5177a；第二父提交是上述官方 rc40。
- VERSION 保持 v1.0.0-rc.40.chuanapi.20260922.integration；本轮没有发布标签或镜像。
- 本轮没有改 main、访问生产数据库、调用真实付费模型、部署或进行基础依赖升级。

本轮是在 6691f29b 的全部定制修正之上增量修改，不是把旧 a1ee61c5 工作树整体覆盖回远端。对本轮触及文件核对过 a1ee61c5 至 0da705d2 的变更范围，保留了 6691f29b 的资金、隐私、真实重试入口及前端契约修正。

## 第一阶段：实际纳入的七个上游修复

以下是补丁级回移及定制适配，整合在本次提交中；不宣称七个上游提交都成为本分支的历史祖先。上游检查范围为 rc40 至 d04c118c8803f49e0c9bab74dcf5b5efeab9464a。

| 原始上游提交 | 纳入内容 | 定制边界与回归 |
|---|---|---|
| d04c118c8803f49e0c9bab74dcf5b5efeab9464a | Responses WebSocket 保存采用统一 supportsResponsesWebSocket 判断 | 保留渠道表单其余逻辑；覆盖支持类型的创建、更新、重载，未开启和不支持类型不误开启。 |
| 5401874c6387b52149b7e5b0f61d9dafea5f5b7a | 独立二进制及 Electron 构建修正 Version 的完整 Go 包路径 | 仅改 linker 注入路径，不改变发布触发条件、不触发发布。 |
| 996adffe5165bd5e311e33a03a86b8aede1fe376 | 价格编辑器打开时保持表格单元格挂载和文字选区 | 不改计费公式；新测试使用定制 model-pricing 的 entries/options/empty_version 返回结构。 |
| 8cb88ebd17a072691f39705ae72c786f8bb5c4e0 | 插件使用中弹窗只有一个取消按钮，强制操作走标准确认/加载状态 | 另补 pending 防重复提交和关闭保护，取消不会再弹旧删除确认，也不自动升级为 force。后端权限与使用中检查未变。 |
| 9310231b3c27fea933e939b46cf26e0ce67192e3 | 日志模型徽章行高与上下留白修复 | 保留模型隐私处理调用方与移动端身份夹具，只调整展示及样式断言。 |
| a46f045d6d146b1e7e17616974bbf37e32885850 | 当前价格与上游价格列使用一致宽度 | 新增单源/多源列宽回归；不修改价格值和表达式。 |
| c76452d22412be45eb708f2618748f7aac46703d | Playground 长输入折行 | 新增长 URL/中英文/代码文本保持、输入法组合和 Shift+Enter 不误提交回归。 |

未纳入 6c14c076 的价格表达式文本差异高亮。它不是计费语义比较，也不是本轮验收必须项。

## 上轮四个待应用文件：已全部写入实际源码

以下目标 Git blob 已逐项核对，原 remaining.patch 不再是交付方式：

| 文件 | 已完成修复 | 目标 blob |
|---|---|---|
| controller/plugin_protocol_test.go | 图片成功响应保持原始模型别名，失败屏蔽供应商细节；既有持久化、断连和结算用例保留 | 32989402b1f1e90b3b4911d3183aa959839662f5 |
| controller/relay.go | 物理删除无人调用的 controller.shouldRetry；保护继续位于 service.DecideRelayRetry | e77f7c9524dc9c1d670d1ad086ba62a209ce53e4 |
| web/src/features/channels/lib/header-passthrough.ts | 版权头空行规范化，行为不变 | b8baa69e2f0d637630dd580308d74254fa2c8825 |
| web/src/features/channels/lib/model-mapping-rules.ts | 版权头空行规范化，行为不变 | 5ea6f014ed83ec80c2a429c52dd528685ea9daba |

## 临时整合工具清理

已删除 rc40-integrate.yml、rc40-diagnostics.yml、rc40-integration-audit.yml、rc40-stage1-review.yml，以及 .github/rc40 下的 replay.py、followup.py、remaining.patch。保留 resolutions.json 作为原始 78 个冲突的历史决定记录；其中摘要产生于当时格式化前，不是最终发布签名。

新增 .github/workflows/rc40-verify.yml，只有 contents: read，checkout 固定 github.sha 且不保存写凭证。流程没有提交、推送、合并、发布或生产操作。每次整合分支 push 均运行只读检查，不再以“先运行脚本修改源码”作为测试前提。

配置的验收包括完整 Go build/vet/test、独立 relaykit、发布验收门测试、前端 typecheck/lint/format/copyright/build/依赖审计、四分片完整 Vitest，以及隔离 SQLite/MySQL 5.7/PostgreSQL 9.6 迁移、支付约束和分账预扣矩阵。最终 acceptance 要求每组结果均为 success；failure、cancelled、skipped 不能放行。

## 本轮实际验证及限制

本地执行并通过：

- 本轮差异的 git diff --check。
- 两个变更 Go 文件的 gofmt 检查。
- 14 个变更/新增 TypeScript 文件的语法解析，0 语法错误。此项不是完整 typecheck，也不是 Vitest。
- 发布验收门 Python 测试 8/8 通过；测试文件在本轮基准范围内未变。
- 可用工作树的版权检查通过。本轮相关 TypeScript 文件均保留原版权头。
- 四个遗留文件的目标 blob 一致性检查。

本地环境的 Go 为 1.23.2，低于项目要求；没有 Bun 和完整前端依赖，网络不可用。因此本地没有执行完整 Go 构建/测试、前端类型检查、lint、格式化器、Vitest 或生产构建。

GitHub 源码检查运行 https://github.com/1057300248/wanchuan-new-api/actions/runs/35943214676 在执行步骤开始前失败，snapshot 作业没有步骤记录。此前第二轮整合验收也存在相同的“未开始步骤”表现。没有足够证据判断具体原因，不猜测账户计费、平台或代码故障。

**最终验收只能依据本次源码提交 SHA 对应的新 RC40 candidate verification 结果；这里不把旧提交通过的检查算作新提交通过。完整运行尚无通过证据，仍为 HOLD。**

## 保留的旧定制及首轮审查结论

| 范围 | 决定 |
|---|---|
| modules/chuanapi、moneyport、composition root | 保留模块化边界与工厂注入，不整体改写。 |
| 分账钱包、账本、支付、订阅 | 保留原子性、幂等、退款及拆分余额约束；新预扣策略需要继续跑完整组合回归。 |
| Cline、CC Switch、API Key | 保留自定义渠道、Key/分组/协议/模型限制，禁止候选刷新自动解除模型限制。 |
| 分组重命名 | 保留 baseline、显式 rename、事务保存和 stale-write 拒绝；409 不丢编辑。 |
| 账号绑定 | 保留提交时身份、邮箱证据和账号状态重新检查，防止并发抢绑和失效会话。 |
| 注册与推广 | 保留 SignupBonus/Affiliate 新字段，未复活旧邀请奖励配置。 |
| 隐私 | 保留普通调用者与显式渠道测试的边界，不因为 admin/root 身份自动暴露渠道诊断。 |
| 模型映射和旧钱包弹窗 | 延续已完成的交互收敛和旧界面删除，不保留两套并行写入逻辑。 |
| 阿里图片旧路径、旧预扣常量 | 已接受 rc40 合并中的替代；新链路仍必须通过功能和计费验收。 |
| 品牌和翻译 | 保留 ChuanAPI 品牌及逐键三方合并后的文案。 |

6691f29b 已修复：真实重试决策入口的失败流保护、新图片协议隐私边界、分账钱包预扣测试夹具、Key 范围模型缓存及分组页面测试契约。本轮没有撤销这些修正。

## 历史验证，不代表当前版本

首轮精确被测提交 a1ee61c51d727f74880d5570628e65f0c9b5177a，运行 https://github.com/1057300248/wanchuan-new-api/actions/runs/35699464113 ：Go build/vet、独立 relaykit、前端构建、生产依赖审计及发布验收门通过；完整 Go 测试、前端 typecheck/lint 未通过；完整 Vitest 247 文件中 241 通过、6 失败，2550 用例中 2520 通过、30 失败。后续修正必须在其自身 SHA 上重新验证。

原始三方审查：共同祖先 69a50029819a26c53e6babd276d49cfe2f8880ad，定制修改 1052 文件、上游 449 文件、重叠 307 文件、78 个冲突路径。首个合并候选相对 main 250 文件、+26212/-14786 行，不能与本轮增量统计混用。完整首轮文字记录可在该文件的 6691f29bd2feb3c89f9be396bfda169218f38176 历史版本查阅。

## 下一道门

取得最终 SHA 全部检查通过并复核真实数据库矩阵前，不进入 main 合并或生产评估。生命周期、连接池、缓存刷新失效处理、限流契约、依赖升级及路由优化不属于此次第一步补丁范围；应另立可回滚变更，避免掩盖 rc40 整合回归。没有生产性能测量，不声称性能提升比例。
