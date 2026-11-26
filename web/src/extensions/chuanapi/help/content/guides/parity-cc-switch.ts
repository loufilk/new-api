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

export const parityCcSwitchGuides: readonly GuideDoc[] = [
  {
    slug: 'cc-switch-common',
    title: 'CC Switch 通用设置',
    description: '保存供应商前核对协议、地址和凭据。',
    body: `
## 先选客户端，再添加供应商

同一把 Key 能调用的模型由分组决定，客户端类型决定要用哪个地址。Claude Code / Anthropic 使用 \`{{BASE_URL}}\`；Codex / OpenAI 使用 \`{{BASE_URL}}/v1\`。模型名按列表中的 ID 填写。

## 保留当前配置

从 [API 密钥](/keys) 行菜单使用独立的 CC Switch 导入入口，或在客户端手动添加提供商。导入前核对名称和域名，避免覆盖官方登录配置；不要转发含有完整 Key 的导入链接。
`,
  },
  {
    slug: 'cc-switch-claude-code',
    title: '在 CC Switch 里配置 Claude Code',
    description: '给 Claude Code 添加一个万川供应商并检查生效情况。',
    body: `
## 添加供应商

在 CC Switch 选择 Claude Code，再用 [API 密钥](/keys) 的 CC Switch 操作导入，或手动新建。配置根地址 \`{{BASE_URL}}\`，将模型设为支持 Messages 的 ID。用不同的供应商名称保留上一套配置。

## 切换并重开会话

切到万川后重开终端会话，确认没有其他 ANTHROPIC 环境变量把配置覆盖。发一条短消息并在 [使用日志](/usage-logs) 核对；需要切回官方时恢复官方提供商登录。
`,
  },
  {
    slug: 'cc-switch-codex',
    title: '在 CC Switch 里配置 Codex',
    description: '为 Codex 选择支持 Responses 的万川路线。',
    body: `
## 创建 Codex 供应商

在 CC Switch 选择 Codex，配置基础地址 \`{{BASE_URL}}/v1\`、独立 Key 和支持 Responses 的模型。不要把完整 /v1/responses 贴到基础地址字段。

## 验证

重启 Codex 或开新会话，确认确实选中新的供应商。如果模型列表显示正常但请求失败，按 [Codex FAQ](/help/guides/faq-codex) 核对 Responses 能力与本地配置。
`,
  },
  {
    slug: 'cc-switch-codex-app',
    title: 'CC Switch 与 Codex App',
    description: '检查桌面版是否读取本机 Codex 配置。',
    body: `
## 先验证 CLI

先按 [CC Switch 配置 Codex](/help/guides/cc-switch-codex) 验证本机 CLI，备份配置。若桌面版支持读取相同的配置与凭据，完全退出 App 后重新启动，再选万川提供商。

## 区分 App 与 CLI 环境

桌面版可能没有继承终端变量，不同设备和运行环境也不会自动共享配置。遇到仅 CLI 成功时，请核对 App 的当前版本是否支持相同配置，而不是改写原厂账户。
`,
  },
  {
    slug: 'cc-switch-claude-desktop',
    title: '在 CC Switch 里配置 Claude Desktop',
    description: '仅针对支持第三方提供商的 Claude Desktop 版本与策略。',
    body: `
## 在正确的客户端标签操作

确认 [CC Switch 当前版本](https://github.com/farion1231/cc-switch) 有 Claude Desktop 标签，且桌面客户端允许外部提供商。新建万川供应商，使用 \`{{BASE_URL}}\` 和支持 Messages 的模型 ID；不要把它导入错误的 Claude Code 标签。

## 重启并核对

完全退出 Claude Desktop 后重开。发送一条短消息并核对使用日志。若组织策略限制外部提供商，保持原策略，改走 [Claude Code](/help/guides/claude-code)。
`,
  },
  {
    slug: 'cc-switch-usage',
    title: 'CC Switch 用量查询与万川对账',
    description: '辨别本地会话统计、密钥限额和钱包真实扣费。',
    body: `
## 以控制台为准

本地 CC Switch 的用量不能代表钱包真实扣费。到 [使用日志](/usage-logs) 按时间、模型、Key 检查万川入账；到 [钱包](/wallet) 查账户余额。密钥的剩余额度和钱包余额是不同概念。

## 不把管理凭据交给第三方

若客户端版本有供应商用量查询功能，只配置已确认支持的授权方式。不要把后台会话、管理员凭据或全权限 Token 粘进通用脚本。客户端无法查询时仍可用万川控制台完成对账。
`,
  },
  {
    slug: 'cc-switch-cli',
    title: 'CC Switch CLI（社区版）',
    description: '在无桌面或远程会话里检查配置目录和版本。',
    body: `
## 区分发行渠道

桌面 CC Switch 和社区维护的终端版本独立发布。安装入口与兼容版本可从 [CC Switch 项目说明](https://github.com/farion1231/cc-switch) 查到。不同版本不一定能读取同一数据库；更换版本前备份原配置，不让两个进程同时写一份运行中的配置。

## 最小化配置和验证

添加新供应商而不是覆盖旧项。按应用类型填写 [相应协议地址](/help/guides/cc-switch-common)，在同一终端中重开目标 CLI。不要从远程服务器复制含有实际密钥的整份配置到公共目录。
`,
  },
]
