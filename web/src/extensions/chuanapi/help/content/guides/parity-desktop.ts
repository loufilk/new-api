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

export const parityDesktopGuides: readonly GuideDoc[] = [
  {
    slug: 'desktop-overview',
    title: '桌面应用与浏览器工具接入',
    description: '按客户端实际支持的协议选择接入方法。',
    body: `
## 按客户端能力选择

- 可自定义 OpenAI 兼容服务的客户端通常使用 \`{{BASE_URL}}/v1\`。参见 [WorkBuddy](/help/guides/workbuddy)、[AionUI](/help/guides/aionui) 和 [Cherry Studio](/help/guides/cherry-studio)。
- 允许配置第三方 Anthropic 服务的客户端使用 \`{{BASE_URL}}\`，由客户端添加 /v1/messages。参见 [Claude Desktop](/help/guides/claude-desktop)。
- 若只需要管理供应商或查询用量，参见 [CC Switch](/help/guides/cc-switch) 和 [All API Hub](/help/guides/all-api-hub)。

## 一把 Key 对应一个用途

为不同应用单独创建密钥、选择分组并按需设置额度。模型名称必须用完整 ID；图像、工具、SSE 等能力要分别验收。第三方客户端保存凭据，用前先确认是否会同步到云端。
`,
  },
  {
    slug: 'claude-desktop',
    title: 'Claude Desktop：区分官方登录与自定义模型',
    description: '用支持外部提供商的配置入口接 Anthropic Messages。',
    body: `
## 选择接入方式

官方账号登录与第三方 API Key 是两套账户。客户端版本和组织策略可能限制第三方提供商；不要把万川 Key 填进官方账号登录页。如果界面支持添加外部 Anthropic 服务，地址填 \`{{BASE_URL}}\`，选择支持 Anthropic Messages 的模型并填写专用 Key。没有该入口时，参见 [CC Switch 的 Claude Desktop 说明](/help/guides/cc-switch-claude-desktop)，或使用 [Claude Code](/help/guides/claude-code)。

## 验证

新开对话，发送一条简短消息后检查万川使用日志。若是自己运行的第三方 MCP 服务，修改 MCP 配置本身不会更换桌面上的对话模型。
`,
  },
  {
    slug: 'workbuddy',
    title: 'WorkBuddy 自定义模型',
    description: '支持配置 OpenAI 兼容提供商的 WorkBuddy 版本可手动接入。',
    body: `
## 确认版本和入口

先确认个人版或企业版是否提供自定义模型入口。企业版的界面可参考 [腾讯配置文档](https://cloud.tencent.cn/document/product/1831/134445)。当前文档只指导手动配置，不提供一键写入 WorkBuddy 的入口。

## 设置字段

选择 OpenAI 兼容格式。若界面问的是基础地址，填 \`{{BASE_URL}}/v1\`；若要求完整 Chat Completions 请求路径，填 \`{{BASE_URL}}/v1/chat/completions\`。模型填写分组可用的 ID，密钥使用万川专用 Key，不与 WorkBuddy 登录或订阅密钥混用。

## 确认调用

保存后从模型列表切到自定义项，发一条简短请求后查看万川使用日志。只显示模型并不代表该客户端已使用它。
`,
  },
  {
    slug: 'aionui',
    title: 'AionUI 接入万川',
    description: '添加兼容供应商或通过已经配置好的代理客户端使用。',
    body: `
## 选择调用方式

从 [AionUI 项目](https://github.com/iOfficeAI/AionUi) 查看当前版本是否提供自定义模型服务设置。如果有，新增 OpenAI 兼容提供商，填写 \`{{BASE_URL}}/v1\`、独立 Key、实际模型 ID。若 AionUI 只是启动已安装的外部 Agent，使用该 Agent 自己的配置，而不是只修改 AionUI 的展示选项。

## 排查

切换到新配置后用文本小样本确认连通，再逐项跑工具调用。确认终端和 GUI 的凭据不是来自两套不同的运行环境。
`,
  },
  {
    slug: 'all-api-hub',
    title: 'All API Hub 管理站点',
    description: '用浏览器扩展管理站点与凭据时，先区分站点登录和模型 Key。',
    body: `
## 添加站点

登录 [万川控制台](/)，在你信任的 All API Hub 扩展里新增站点，站点地址填 \`{{BASE_URL}}\`。如版本需手动选择识别方式，仅当扩展列出兼容 New API 时再选。遇到自动检测失败时，仍可在 [API 密钥](/keys) 和 [使用日志](/usage-logs) 查看数据。

## 在应用里使用模型

若扩展支持单独的 OpenAI 兼容连接配置，地址使用 \`{{BASE_URL}}/v1\`。浏览器会话、余额查询凭据、模型 Key 不应相互替代。不把密钥导出到公共文件或共享同步盘。
`,
  },
  {
    slug: 'gpt-claude-code',
    title: '在 Claude Code 中使用兼容的 GPT 模型',
    description: '只有提供 Anthropic Messages 能力的 GPT 路由才适用。',
    body: `
## 检查协议，不要只看厂商名称

在 [模型广场](/pricing) 找目标 GPT 模型并核对 Anthropic Messages 端点和分组。只有 OpenAI Chat 或 Responses 的路由不能直接给 Claude Code 使用。

## 配置和测试

参考 [Claude Code 接入](/help/guides/claude-code)，以 \`{{BASE_URL}}\` 为根地址，选择目标模型的精确 ID。先验证 [Messages](/help/api?endpoint=claude-messages) 的短文本，再测试流式和工具。厂商标签相同也不表示上下文、工具或思考模式与原版 Claude 相同。
`,
  },
  {
    slug: 'deepseek-claude-code',
    title: '在 Claude Code 中调用兼容的 DeepSeek',
    description: '先验证分组的 Anthropic Messages 支持，再接 Claude Code。',
    body: `
## 确认可调用路径

用分组 Key 查询模型 ID，然后在 [模型广场](/pricing) 核对目标 DeepSeek 路由是否有 Anthropic Messages。仅支持 Chat Completions 的分组应换用 OpenAI 兼容客户端。

## 设置并检查

按 [Claude Code](/help/guides/claude-code) 写入根地址和模型选择；不要把 /v1/messages 完整路径填成根地址。新会话先测试短文本，再测带工具的任务。工具请求返回不兼容时，先保留原配置，再选另一个明确支持该能力的路由。
`,
  },
  {
    slug: 'deepseek-codex',
    title: '在 Codex 中使用兼容的 DeepSeek',
    description: '区分 Codex 使用的 Responses 与一般 DeepSeek Chat 接入。',
    body: `
## 选择支持 Responses 的组

[模型列表](/help/api?endpoint=models-list) 只说明当前 Key 看得到模型。目标 DeepSeek 路由还要支持 [Responses](/help/api?endpoint=responses) 才能走 Codex 的 Responses 模式；不要用 Chat Completions 地址冒充 Responses。

## 使用独立配置

按 [Codex CLI](/help/guides/codex-cli) 新增万川提供商，填 \`{{BASE_URL}}/v1\` 与精确模型 ID，把密钥放在专用环境变量。先测试短文本及一次只读工具操作。若所选分组仅提供 Chat，改用 [OpenCode](/help/guides/opencode)。
`,
  },
]
