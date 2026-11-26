/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import type { GuideDoc } from '../types'

export const parityCliGuides: readonly GuideDoc[] = [
  {
    slug: 'cli-environment',
    title: '环境检查：安装 CLI 前先核对什么',
    description: '检查终端、客户端版本、HTTPS 连通性与 API 密钥。',
    body: `
## 检查本机环境

在实际运行工具的环境里打开终端。Windows 的 PowerShell、WSL 和外部 IDE 通常不是同一个环境；不要混用它们的安装目录或环境变量。按客户端官方安装说明检查运行时版本；执行工具自身的版本命令确认可执行文件在 PATH 中。

## 先排除地址和网络问题

检查 DNS 和 HTTPS 是否可达；仅验证连接时不要附带令牌：

\`\`\`sh
curl -I --max-time 10 {{BASE_URL}}/
\`\`\`

站点能够打开不代表模型可用。再用自己的 Key 查询 [模型列表](/help/api?endpoint=models-list)，不要把 Key 粘进命令历史或截图。OpenAI SDK 一般使用带 /v1 的 Base URL，Claude Code 使用站点根地址。若客户端仍报错，请看 [缓存与代理排查](/help/guides/cli-cache-fix)。
`,
  },
  {
    slug: 'kimi-code',
    title: 'Kimi Code CLI 接入',
    description: '为 Kimi Code CLI 配置支持 OpenAI Chat 的万川模型。',
    body: `
## 安装并备份

按 [Kimi Code 官方安装说明](https://github.com/MoonshotAI/kimi-code) 安装，然后确认终端能执行 kimi。备份当前用户配置，另建一个万川提供商，不要覆盖正在使用的 Kimi 官方登录。

## 选自定义提供商

打开 [官方提供商配置](https://www.kimi.com/code/docs/en/kimi-code-cli/configuration/providers.html)，按你的 CLI 版本选择 OpenAI Chat 兼容提供商。填入 Base URL：\`{{BASE_URL}}/v1\`。模型名使用 [模型广场](/pricing) 上的 ID，并用 [模型列表](/help/api?endpoint=models-list) 确认当前 Key 能看到它。把密钥放在本地专用环境变量或客户端支持的密钥存储中。

## 验证选择

启动 CLI 后切到新添加的万川模型，并发送一个简短请求。若客户端使用的是 OpenAI Responses 配置而模型仅支持 Chat，首个请求可能失败；先确认选择的提供商协议。万川 Key 不适用于 Kimi 的订阅登录。
`,
  },
  {
    slug: 'grok-build',
    title: 'Grok Build CLI 配置与兼容性',
    description: '区分 Grok Build 的模型发现与实际推理，避免接错线路。',
    body: `
## 核对客户端版本

安装方法和账号要求以 [Grok Build 官方文档](https://docs.x.ai/build/overview) 为准。确认使用的就是 Grok Build CLI，而不是旧版 Shell 或桌面工具。

## 先确认模型请求的出口

客户端能读自定义模型列表，并不等于它把推理请求也发往同一地址。仅当当前版本文档明确支持把实际推理接到外部 OpenAI 兼容服务时，才配置万川的 \`{{BASE_URL}}/v1\`，并选本 Key 可用的模型。不能把万川 Key 用作 xAI 原厂登录凭证。对于只能切模型列表、不能切推理地址的版本，改用 [OpenCode](/help/guides/opencode) 或 [OpenAI SDK](/help/guides/openai-sdk)。

## 验证请求来源

用低成本模型发一条短消息，在万川 [使用日志](/usage-logs) 确认模型和 Key。如果只有客户端显示成功而万川无对应请求，不要把它视为接入通过。
`,
  },
  {
    slug: 'opencode',
    title: 'OpenCode：接入自定义提供商',
    description: '选择第三方 OpenAI 兼容配置，并从终端使用万川模型。',
    body: `
## 添加提供商

根据 [OpenCode 提供商配置文档](https://opencode.ai/docs/providers) 增加一个 OpenAI 兼容提供商。OpenCode 大版本间的设置结构有区别，请使用当前版本配置示例，不要直接覆盖整份已有配置。

- API 地址：\`{{BASE_URL}}/v1\`
- Key：使用 OpenCode 支持的环境变量引用或本地凭据存储。
- 模型：用模型列表中完整的模型 ID，不要用展示名称替代。

## 切换并检查

在客户端选择新提供商下的模型，先试普通文本，再按需测试工具调用。某个模型能对话，不代表对应套餐或协议支持工具调用。检查万川 [使用日志](/usage-logs) 中是否是预期的模型和 Key。
`,
  },
  {
    slug: 'openclaw',
    title: 'OpenClaw 模型接入',
    description: '为指定代理配置自定义模型，而不是替换整个网关的默认配置。',
    body: `
## 添加万川提供商

按 [OpenClaw 自定义提供商文档](https://docs.openclaw.ai/gateway/config-tools/custom-providers) 添加一个新提供商。仅用支持的 OpenAI Chat 兼容模式，Base URL 填 \`{{BASE_URL}}/v1\`。模型 ID 要和 [模型列表](/help/api?endpoint=models-list) 一致；密钥放在 OpenClaw 支持的本地凭据中。

## 从单个代理验证

先给独立测试代理选新模型，给它一把单独的限额 Key。检查它能正常回一条短消息再改默认配置；若工具调用失败，核对模型能力和客户端请求格式。不要为接入关闭 TLS 证书校验或放宽网络访问权限。
`,
  },
  {
    slug: 'hermes-agent',
    title: 'Hermes Agent 自定义模型',
    description: '在本地配置自定义 OpenAI 兼容服务并验证实际路由。',
    body: `
## 选择自定义 API

按 [Hermes Agent 官方说明](https://github.com/NousResearch/hermes-agent) 安装当前版本。在模型设置中选择 Custom / OpenAI-compatible 提供商，把 API 地址设为 \`{{BASE_URL}}/v1\`，填写万川模型 ID。将专用 Key 存在 Hermes 文档指定的凭据位置，只让需要它的本地进程读取。

## 验证并保留回退路径

启动新会话，关闭非必要工具，只发一条短消息；在万川控制台核对使用日志。若某个模型只能文本对话，换到明确支持工具的分组后再测。原有提供商与配置应单独保留，以便恢复。
`,
  },
]
