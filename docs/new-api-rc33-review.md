# new-api rc.33 运营前审计与整改结果

## 结论

本地候选已合并官方 rc.33 并完成下列整改，但本轮没有满足生产发布的全部验收条件。
结论是 **候选可进入隔离验收，暂不签发正式运营放行结论**。
尚缺真实 MySQL/PostgreSQL、生产旧库恢复、race、签名镜像、72 小时 canary、容量和完整线上用户旅程证据。

## 主要发现

### P1：发布证据曾指向旧候选和旧回滚版本

历史 rc30 门禁样例包含既有 PASS 值，恢复脚本绑定 rc26、MySQL 8.4 和固定目录；替换版本字符串不足以证明 rc33 可发布。
本轮早期对这些历史脚本的版本替换已经撤回。历史资产保留原用途，不进入 rc33 的发布判定。
新增独立只读门禁 `ops/server/new-api-release-gate.py`，强制候选 commit/digest、证据文件 SHA256、完整检查集合和 72 小时观测。
样例所有检查均为 PENDING；包括真实三数据库、日志库、rc31 回滚、支付/资金、签名、SBOM 和依赖审计。
该门禁校验完整性与关联关系，不替代人工日志审查或 cosign 验签。缺证据始终返回非零退出。

### P1：支付回调日志泄露正文与签名

原 Stripe、Creem、Waffo/Pancake 记录完整 body/signature；易支付还将签名写入参数和 URL 日志。
现有生产沿用旧代码，因此本地修复尚未降低线上的该项暴露面。
候选将正文替换成长度/短 SHA256，签名仅保留是否存在；易支付只保留参数数量。
支付回调 handler 与访问日志均移除 query，真实 query 保留给验签，Creem 客户姓名/邮箱和 checkout URL 不再进入相关成功日志。
回归测试实际调用 Stripe、Creem、易支付 GET/POST handler，断言错误行为不变且日志中无测试敏感值。
位置：`controller/topup_stripe.go:184`、`controller/topup.go:464`、`middleware/logger.go`。
这不是全系统日志脱敏认证；其他提供商错误正文、历史日志保留与运维日志访问仍需生产复核。

### P1：依赖审计发现 7 条公告，已在候选消除

真实执行 Bun audit 后，发现 nanoid 3.3.16、Mermaid 11.15.0 和 decode-uri-component 0.4.1 的传递依赖告警。
定向更新 Mermaid 11.16.1、decode-uri-component 0.5.0，并将 PostCSS 固定到 8.5.28，使其依赖 nanoid 3.3.18。
应用直接使用的 nanoid 5.1.16 保留；仅修改三个 overrides 和对应锁文件条目，没有全量升级依赖。
更新后 `bun audit --production --json` 返回 `{}`、退出码 0。
公告等级不等于已证实的生产可利用性；本轮未对线上实施漏洞利用验证。
依据：[nanoid](https://github.com/advisories/GHSA-2v37-7h3g-55p8)、[Mermaid](https://github.com/advisories/GHSA-c4c3-pg64-4m4v)、[decode-uri-component](https://github.com/advisories/GHSA-vcc3-ghjq-m6fr)。

### P1：新部署模板的凭据、监听与来源约束不足

仓库通用 Compose 原有默认密码、latest 和全接口端口发布。它不是当前生产实际 Compose 的证据，不能据此断言线上使用默认密码或直接暴露 3000。
候选模板现在要求显式镜像/凭据/会话 Origin/代理信任参数，绑定 loopback，增加依赖健康检查、日志轮转和 150 秒排空期。
Compose 的 `${VAR:?}` 只检查非空；镜像 digest 真正有效性、来源与签名仍需发布门禁和操作人员核对。
已有生产必须保留原数据库用户、DSN、密钥与数据卷，不得用此新部署模板覆盖现有配置。

### P2：健康状态不足以代表依赖可用

`/api/status` 读取内存配置并返回成功，不是数据库或 Redis 就绪证明。
候选新增独立 GET/HEAD 探针，healthz 不经过 Redis 限流；readyz 检查主库、独立日志库和启用的 Redis，共享两秒超时。
返回仅含 success，失败 503，且 no-store；Caddy 公网阻止直接访问，避免暴露内部依赖探测入口。
已通过路由级依赖故障测试，包括无数据库、数据库关闭、独立日志库关闭、Redis 缺失/错误和健康情况。
位置：`controller/misc.go:188`、`router/api-router.go:15`、`ops/server/Caddyfile:45`。

### P2：运行时与本地验证边界

- pprof 开启时原绑定所有接口，候选收紧到进程所在网络命名空间的 loopback；这不代表线上曾启用 pprof。
- HTTP Server 增加 15 秒请求头读取、120 秒空闲超时；不添加会中断正常 SSE 的总 WriteTimeout。1 MiB 头部上限是显式写出 Go 默认值，不把它计为额外限额缩减。
- local/backups 内 Go 文件曾污染标准 ./... 包发现。本地资料分区新增忽略的嵌套 go.mod，根模块标准测试通过；生产干净源码不含 local，make test 继续检查全部业务包。
- .mjs 缺少 LF 属性，Windows 干净 checkout 也会在三个脚本上产生格式假失败。补充 .mjs/.cjs LF 属性，验证目录仅规范化换行后格式检查通过；原工作区三个脚本逐字节保留。
- 数据库 CI 原仅触发旧合并分支；现在覆盖 PR、main 和 codex 候选分支。普通 CI 增加生产构建、依赖审计与证据门禁测试。

## 版本与保留行为

官方目标固定为 `eb99ab1b40343c3317bb47981cccdbb2b159a5fa`，不跟随未发布 main。
最初公开 rc31 tag 不是最新实际部署基线；当前分支已含 reasoning 和 Responses 后续修复。
合并保留 Responses 错误终态与禁止错误后重试、钱包分账/支付幂等、路由、插件和管理员价格覆盖；接入 rc33 的内置计费默认值与模型列表。
本轮未修改 model 模型/迁移/ORM驱动版本。未将上游实验性插件警告解释为已经满足生产稳定性要求。

2026-09-06 12:38:23 UTC，只读 GET `https://chuanapi.com/api/status` 返回 HTTP 200 / success=true：
`v1.0.0-rc.31+main.7c044d7.chuanapi.20260906.responses.8eee45fd2`。
本轮未 SSH 修改生产配置、变更数据库、收费调用上游、发送邮件、发起付款或启动线上 canary。

## 实际验证

| 检查 | 结果与边界 |
| --- | --- |
| 根模块 `go test -count=1 ./...` | 通过；Go 1.26.5 / Windows amd64；含更新后的探针与支付回归 |
| 根模块 `go vet ./...` | 通过 |
| `relaykit` 独立 `GOWORK=off go test ./...` / `go build ./...` | 通过 |
| `TEST_DATABASE_COMPAT=1 TEST_DATABASE_DIALECT=sqlite go test ./model -run '^TestDatabaseCompatibilityReal$' -count=1 -v` | 通过；真实 SQLite 3.50.4；双次启动迁移、CRUD、大数和支付幂等 |
| 实际 MySQL/PostgreSQL/独立日志库恢复矩阵 | 未执行；当前主机没有 Docker 或对应数据库实例；不声明三数据库兼容完成 |
| race | 未通过运行前提；CGO_ENABLED=1 后报告 gcc 不在 PATH；需 Linux/C 编译器环境复跑 |
| 前端 frozen install | 干净 worktree 安装通过；本地 Bun 1.3.13，仍需 Dockerfile 固定 Bun 1.4.0 复验 |
| 前端测试 | 依赖修复后 107 文件 / 623 用例通过；jsdom scrollTo 提示保留，不作为浏览器验证 |
| typecheck / lint / format / copyright / build | 干净验证目录全部退出 0；Rsbuild 2.1.6；LF 规范化后格式检查通过 |
| `bun audit --production --json` | 依赖修复后 `{}` / exit 0；较早空输出的“通过”口头结论作废 |
| 新证据门禁测试 | 7 组通过；不表示生成过真实 72 小时观测 |
| Caddy 路由契约 | 6 项静态测试通过；不是 Caddy 实例的运行期验收 |
| Compose YAML | 静态解析通过；未执行 Docker Compose 插值、启动或容器网络验证 |
| 签名镜像 / SBOM / 72小时 canary / 全量线上旅程 | 未执行，不填写 PASS |

历史 `ops/server/tests` 全组曾有 17 个失败、30 个通过，主要因缺少旧备份脚本/跨仓库路径及 Windows Shell。
这些失败未被删测试或伪造为通过；新门禁与现有 Caddy 契约单独核验。
浏览器桌面/手机、真实登录/2FA/OAuth、真实支付回调及付费上游 smoke 仍属上线前验收，而不是本轮已完成项。

## 后续优先级

1. 在隔离 Linux 构建环境完成固定工具链、race、三数据库旧库恢复与 rc31 回滚，收集主库和独立日志库证据。
2. 为最终提交构建并验证签名/digest/SBOM；更换提交或镜像后不能复用旧候选的 PASS。
3. 完成隔离业务回放与目标容量测试，再开始空载 72 小时观测；本轮未测定可承诺的 QPS、并发流或运营 SLA。
4. 生产只读核验 Caddy/Cloudflare 实际配置、备份恢复时效、告警触达和关键业务流程，最后决定运营放行。
5. 前端构建报告总计约 58 MB 未压缩 / 16.8 MB gzip，包含所有异步模块，不代表首屏负载；继续优化前先测真实首屏和路由加载数据。

操作步骤及证据格式见 `docs/new-api-rc33-production-runbook.md`。本报告不把模板配置、历史日志或单次 HTTP 200 当作生产全量审计完成。
