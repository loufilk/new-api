# im.17 前端与桌面依赖验收

2026-11-19 对照 npm 官方 registry 的稳定 `latest` 检查了 Web 的 86 项、Electron 的 3 项直接依赖（含开发依赖）。其中 85/89 项与 `latest` 一致。Bun 维持 1.4.4；两端的冻结 lockfile 和第三方许可清单已同步。

| 未升到 latest 的直接依赖 | 锁定值 | 当日 latest | 原因与独立迁移工作 |
|---|---:|---:|---|
| `@tanstack/react-table` | 8.21.3 | 9.3.1 | v9 主入口不再导出 `useReactTable`。仓内 93 个文件直接导入此包；不仅要换构造函数，也要迁移行模型、受控状态和渲染订阅，并回归排序、选择、筛选、分页。8.21.3 是最终 v8 稳定版。 |
| `i18next` | 26.7.2 | 27.2.1 | v27 收紧翻译回调的 options 重载，全站 57 处类型错误。以对应主版本最新补丁维持行为，未对类型断言放宽。 |
| `react-i18next` | 17.1.13 | 18.0.3 | v18 的 peer 要求 i18next ≥27.0.1，须与上一项同时迁移。 |
| `oxlint` | 1.74.0 | 1.94.0 | lint:ci 校验锁定的工具版本及上游文件指纹。升级需要专项处理 React Compiler 规则与新诊断；未修改基线或关闭规则来跳过验收。 |

间接依赖按消费者兼容范围解析，而非强制全部使用最高主版本。例如 ajv 使用 fast-uri 3.1.9；Node 工具按需并存 undici 7.30.2 / 8.12.4。更新两端的 minimatch 修复版本，同时保留旧 Electron 工具实际需要的 3.x/5.x 调用方式。Web 的 `bun audit --json` 和 Electron 的 `npm audit --json` 均未报告已知告警；不等于完整安全审计。

## 直接影响用户的兼容修复

- LobeHub 图标 v6 的实体文件改为 `.mjs`。修改懒加载目录和分包过滤器，既有 Linux/Windows 路径测试现在从实际安装包取样，不再基于旧文件名构造样例。复核正式构建的图标加载表，包括 OpenAI、Claude、Gemma、LobeHub 特殊样式。
- 语言检测器会在转码之前剔除带空格的值。恢复旧版保存的语言偏好时先归一为支持列表中的编码，避免繁中用户升级后被切回英文；读取存储受限时仍可按浏览器语言初始化。
- jsdom 31 能计算 Vaul 的出场动画名，但不播放 CSS 动画。仅在前端单测环境为 Drawer 关闭该动画，让组件按真实关闭状态卸载；不改线上动效，不放宽关闭断言。
- Electron 45 按需下载发行包。打包脚本先确保本机 Electron 二进制与 Chromium 许可在位，避免从全新工作目录打包时遗漏现有分发清单。

## 复验边界

Web：Node 26.13.1 LTS、Bun 1.4.4；使用 `bun install --frozen-lockfile`、`bun check -b --singleThreaded`、`bun run lint:ci`、`bun run build`。Vitest 用 `bun run test` 而非 Bun 原生 `bun test`。

Electron：45.4.1、electron-builder 26.18.0、cross-env 10.1.0。已在 Linux 用 npm ci、npm ls、npm audit、JS 语法检查和不发布的 unpacked 打包验收；核对包内网关、Electron 及 Chromium 许可。打包检验使用已构建的旧版 Linux 网关作资源夹具，不代表 im.17 的 Windows/macOS 签名、正式安装程序或 GUI 流程已经验证。
