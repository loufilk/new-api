# new-api rc.33 发布与验收手册

## 状态与边界

目标固定为官方 v1.0.0-rc.33 / eb99ab1b40343c3317bb47981cccdbb2b159a5fa。
本轮交付本地候选代码、测试及证据门禁；未推送镜像、启动线上 canary 或发布生产。
官方仍将插件系统标为实验性，发布版不等于生产就绪：[发布说明](https://github.com/QuantumNous/new-api/releases/tag/v1.0.0-rc.33)。

2026-09-06 12:38 UTC 只读访问公开状态，线上仍为 rc.31 Responses 定制版。
历史回滚基线为 8eee45fd2865d53cb9a384573a73e2fa238ee783；生产镜像记录：
wanchuan/new-api:responses-8eee45fd2-20260906。
执行升级前需重新读取实际 image ID/digest、Compose、运行参数和数据库版本，保留当前会话密钥及业务数据。

## 冻结与构建

1. 固定最终通过检查的提交，使用干净 checkout/worktree，勿打包本地归档或凭据：
~~~sh
CANDIDATE_COMMIT="$(git rev-parse HEAD)"
git archive --format=tar.gz --output=source.tar.gz "$CANDIDATE_COMMIT"
sha256sum source.tar.gz
~~~
生成的源归档没有 web/dist，先构建前端，再运行需要 embed 的 Go 检查。
不要在现有脏工作区执行 reset、clean 或覆盖脚本。

2. 按 Dockerfile 固定的 Bun 1.4.0 / Go 1.26.1 Linux 工具链验证；race 单独使用带 C 编译器的构建容器：
~~~sh
(cd web && bun install --frozen-lockfile && bun run typecheck && bun run test \
  && bun run lint:ci && bun run format:check && bun run copyright:check && bun run build)
GOWORK=off go vet ./...
GOWORK=off go test -count=1 ./...
GOWORK=off CGO_ENABLED=1 go test -race ./...
(cd relaykit && GOWORK=off go test -count=1 ./... && GOWORK=off go vet ./... && GOWORK=off go build ./...)
python3 -m unittest discover -s ops/server/tests -p test_release_gate.py -v
~~~
保留每条命令的退出码、工具版本、完整日志及 SHA256；子步骤失败即停止，禁止用最后一条命令的退出码覆盖前面的失败。

3. 构建候选时设置 SOURCE_REVISION 和 RELEASE_VERSION 两个 build args。使用现有 Buildx SBOM/provenance 与 cosign 流程发布到自己的镜像仓库，保存：
源码校验值、构建日志、架构、镜像 digest、SBOM、provenance、签名验证输出。
验证签名时明确 certificate identity、OIDC issuer 或已信任公钥；不得使用任意 identity 匹配。
本轮没有执行镜像发布或签名，示例 digest 不是已发布产物。

## 数据与业务验证

- 三数据库必须是真实 SQLite、MySQL 5.7 和 PostgreSQL 9.6，以及实际生产使用的 PostgreSQL 版本。
  仓库 database-compat 工作流已覆盖普通 PR 和主线/候选分支；启动两次和支付并发测试可通过下面入口执行：
~~~sh
TEST_DATABASE_COMPAT=1 TEST_DATABASE_DIALECT=sqlite GOWORK=off \
  go test ./model -run '^TestDatabaseCompatibilityReal$' -count=1 -v
~~~
MySQL/PostgreSQL 使用同一入口，设置 TEST_DATABASE_DIALECT 和测试专用 TEST_DATABASE_DSN；
只对一次性测试数据库执行。该入口不替代旧库恢复/升级/回滚演练。

- 对每个数据库，先用 rc.31 定制基线创建有代表性数据，再备份并恢复到隔离实例；候选启动两次。
  对账用户各子账户、兼容总额、账本幂等记录、订单、Token、渠道、任务、价格、索引及唯一约束。
  单独配置日志库的路径也需覆盖。演练使用旧应用回退已升级库，证明兼容之后才允许应用级回滚。
- 生产 PostgreSQL dump、Redis RDB、Compose、环境和密钥备份保持在 root 受限目录，不下载到源码仓库。
  Redis 快照使用已验证的非阻塞备份流程，避免同步 SAVE 阻塞线上。
- 核对显式模型 modifier、OpenRouter 真实 -thinking 模型及旧别名映射、管理员定价优先级、Responses 成功/失败/缺终态/重试、任务失败退款和支付重复回调。
  认证覆盖登录、2FA、OAuth、刷新/撤销和 Token 归属。注入失败与支付回放只在隔离环境进行。
- ops/server/new-api-rc30-* 是历史 rc.26 -> rc.30 演练资产，本轮对其版本替换已撤回。
  它们的旧 PASS 值、数据库版本、rc26 回滚和目录命名不能用于证明 rc.33 验收通过。

## 证据门禁

正式入口为 ops/server/new-api-release-gate.py，模板为 new-api-rc33-gates.example.json。
模板所有检查都是 PENDING；它只验证证据，不执行 Docker、迁移、部署或修改控制面。

候选必须包含完整 commit、rc.33 版本、不可变 repository@sha256 镜像与源码归档。
每项 PASS 记录结构如下；证据只能指向 manifest 目录内的非空文件：
~~~json
{
  "status": "PASS",
  "commit": "CANDIDATE_COMMIT",
  "image": "wanchuan/new-api@sha256:CANDIDATE_IMAGE_DIGEST",
  "completed_at": "COMPLETION_TIME_WITH_TIMEZONE",
  "evidence": [{"file": "logs/check.log", "sha256": "CHECK_LOG_SHA256"}]
}
~~~
填写 PASS 表示已人工核对该日志的命令、结果和候选身份。哈希校验不能替代语义审查或 cosign 验签。

Canary 使用独立数据库/Redis和内部网络，不挂生产卷、不发送真实客户流量。
禁止邮件、付款请求和生产上游访问；先完成隔离业务回放，再开始空载稳定性观察。
每五分钟采集一条 JSONL，读取实际容器、探针、日志及数据库，记录：
~~~json
{
  "at": "OBSERVED_AT_WITH_TIMEZONE",
  "commit": "CANDIDATE_COMMIT",
  "image": "wanchuan/new-api@sha256:CANDIDATE_IMAGE_DIGEST",
  "version": "CANDIDATE_VERSION",
  "running": true, "liveness": true, "ready": true, "oom": false,
  "restarts": 0, "critical_count": 0, "funds_mismatch_count": 0,
  "funds_snapshot_sha256": "FUNDS_SNAPSHOT_SHA256"
}
~~~
该格式示例不是观测证据。门禁要求不少于 800 条、首尾跨度至少 72 小时、相邻间隔不超过 10 分钟、
最后一次不旧于 10 分钟、无重复/倒序/未来时间、身份一致、故障计数为零，空载资金快照不变。
空载稳定性不代表负载能力，发布前仍需在隔离环境完成容量和端到端测试。

~~~sh
python3 ops/server/new-api-release-gate.py /path/to/evidence/manifest.json \
  --commit "$CANDIDATE_COMMIT" --image "$CANDIDATE_IMAGE"
~~~
SOURCE、Go/race/relaykit、前端、三数据库、独立日志库、rc31 回滚、资金、协议、插件、上游复核、
签名和 SBOM 缺少任何一项，命令以非零退出。更换候选、镜像或新增补丁后重新收集证据。

## 生产配置与回滚

- 根 docker-compose.yml 为新部署模板，不得覆盖现有生产 Compose 或 .env。
  生产原有 PostgreSQL/Redis 用户、DSN、密钥、网络与卷名全部保留，不用模板的 root/postgres/redis 替换。
- 新部署需显式配置 NEW_API_IMAGE、POSTGRES_IMAGE、REDIS_IMAGE 的经验证 digest，以及数据库/Redis口令。
  会话需要稳定高熵 SESSION_SECRET、Secure Cookie、精确 HTTPS Origin；
  TRUSTED_PROXIES 必须是实际代理地址或网段，不照抄示例网段。端口只发布到 loopback。
- 内部 GET/HEAD /api/healthz 不依赖 Redis；/api/readyz 检查主库、日志库及启用的 Redis，返回 200 或 503 且 no-store。
  Caddy 阻止公网访问这两个内部探针；公开可用性检查使用 /api/status、/public-api/status 和未登录 /v1/models 的 JSON 401。
- 备份记录明确一个 BACKUP_DIR、原 image ID 和已核对的 Compose 路径；不使用 backup-* 通配符选择回滚点。
  候选出现计费/数据不变量错误、依赖故障、持续 5xx 或流式终态回归即停止发布。
  当前候选没有模型/迁移/ORM依赖变更，但仍需恢复演练证明应用级回退可行。
- 后续经确认发布时，仅替换 new-api 镜像及经审核的探针配置，使用 --no-deps --pull never；
  给旧请求排空至少 150 秒，不重启数据库/Redis/Grok2API/Sub2API。
  回滚使用记录的精确旧配置和旧镜像，不把生产数据库覆盖成演练副本。

实际完成的检查和剩余风险见同目录 new-api-rc33-review.md；不得从此手册推断生产已升级。
