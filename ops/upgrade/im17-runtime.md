# 运行时升级候选（2026-11-19）

此分支构建网关、提供测试镜像及示例配置，不更新任何在运行的生产容器。历史验收/旧版兼容矩阵保留原有服务版本。

| 组件 | 候选版本 | 依据 |
|---|---|---|
| PostgreSQL | 18.6 → 18.7 | [官方支持版本](https://www.postgresql.org/support/versioning/)；19 RC2 不是正式版 |
| Redis | 8.8.0 → 8.10.3 | [官方正式发布](https://github.com/redis/redis/releases/tag/8.10.3)；不使用 8.12 RC |
| Go 构建镜像 | 1.27.3 → 1.27.4 | [Go 发布记录](https://go.dev/doc/devel/release#go1.27.4) |
| Bun 构建镜像 | 1.4.4，无需变更 | [Bun 1.4.4](https://github.com/oven-sh/bun/releases/tag/bun-v1.4.4) |
| Node.js 构建与 Electron 打包工具链 | 26.13.1 LTS | [Node.js 官方发行版](https://nodejs.org/en/about/previous-releases)；Electron 自身内置的 Node 由 Electron 发布决定 |
| Debian 运行镜像 | bookworm-slim → trixie-slim | [Debian 稳定发行版](https://www.debian.org/releases/stable/) |

镜像按多架构清单 digest 固定，见 Dockerfile、开发 Compose 和 .env.example。生产 Compose 的镜像和密码仍需显式指定。开发 Compose 针对 PG18 使用新的 dev_pg18_data 卷；如要保留旧 PG15 开发数据，请用原版本导出，再在独立 PG18 卷恢复。不要把 PG15 数据目录直接挂进 PG18。

## 生产变更前的独立验收

1. 盘点实际运行的应用、PG/Redis 镜像 digest、挂载源与目标、PG 扩展、Redis 持久化配置/模块。在数据库中查询 `SHOW data_directory`，确认该目录确实落在已知持久卷上。不要用示例覆盖线上配置。
2. 用一致的完整备份或停写后的快照在**隔离环境**演练恢复，记录对账基准（钱包、风控、路由、日志、任务）和数据时间点。Redis 启用 AOF 时备份完整 AOF 目录及清单而非单个文件；保留原版可恢复的数据副本。
3. **PG18 挂载：**[官方镜像](https://hub.docker.com/_/postgres) 默认 `PGDATA=/var/lib/postgresql/18/docker`。仅全新安装可用示例的 `POSTGRES_VOLUME_TARGET=/var/lib/postgresql`。已有安装不自动换挂载点：先检查真实数据位置、卷内目录结构以及现有 Compose override；不要让新镜像在另一个空目录初始化。若发现旧容器在匿名卷/不同卷或旧布局，先规划恢复到独立新卷并验证，不能仅改一个变量。
4. PG 18.6 → 18.7 是同一大版本，通常不要求导出重导。但[18.7 升级说明](https://www.postgresql.org/docs/release/18.7/)明确指出部分索引可能需要 REINDEX；核对受影响的索引和 C 扩展，切换后重新做全量备份作为增量备份新基线。不把旧增量链作为唯一回退手段。
5. Redis 镜像切换前确认实际 `dir`、AOF/RDB、挂载卷与所有已加载模块。若依赖非核心模块，用相同模块及版本单独演练。按单一变量分别升级数据服务和网关，记录服务版本、就绪状态、错误率、首 token 耗时、账务核对结果。任一失败，停止推进。
6. 回退网关使用原应用 digest；数据库/Redis 回退使用原镜像和与其匹配的经验证备份，不能假设旧 Redis 能打开新版本重新生成的文件。在冻结写入时做好最终同步；若候选已产生新写入，先评估并保全这些写入，避免直接恢复旧快照导致业务数据丢失。

## 本轮验证与边界

- PostgreSQL：18.6 初始化隔离数据库、写入带主键的测试行、正常停库；同一目录用 18.7 重启后核对原值。两版均未启用 ICU/readline/zlib。这是小版本文件格式冒烟测试，不替代生产扩展、真实备份恢复、应用迁移和对账验收。
- Redis：8.8.0 分别生成 RDB 和 AOF；8.10.3 打开同一份对应数据，验证字符串、哈希、到期时间和读取该哈希的 Lua 脚本。两版均为独立源码构建，使用 GCC 14.2、jemalloc、相同优化配置；不含额外模块和 TLS。
- 可重复取得相同源包：[PostgreSQL 18.7](https://ftp.postgresql.org/pub/source/v18.7/postgresql-18.7.tar.bz2.sha256) 的 tar.bz2 SHA-256 是 `441dfae4280cae3e0425e5f876f80559fb0731c140f1aae22b3e5a54d0411bfd`；[Redis 官方校验表](https://github.com/redis/redis-hashes) 中 8.8.0 和 8.10.3 tar.gz 分别是 `88422181efb0c9c0abba332e3e391d409e1e13714b838931669235e5796f704b` 和 `edde778f6bdee7581e42fc139ca9e07e2822212092f7501f99989656e3629d02`。先校验再构建，原生服务只监听回环地址。
- Compose CLI 5.8.0 对开发环境、生产默认及可选 PG18 挂载路径均通过 `config` 解析，生产缺少数据库镜像会失败。按 digest 校验了五个构建/运行镜像的多架构清单、linux/amd64 清单及配置。此项不等于 Docker 镜像已构建、部署或生产切换。
