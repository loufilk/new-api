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

export const paritySupportGuides: readonly GuideDoc[] = [
  {
    slug: 'images-overview',
    title: '图像生成的两种接入方式',
    description: '比较 OpenAI Images 和 Gemini 原生格式，选对协议再调用。',
    body: `
## 先看任务和模型

访客可在 [模型广场](/pricing) 了解图片模型，登录后到图片生成页面试用可见模型。自写客户端时，从模型卡核对端点，使用对该模型有权限的 Key。

- OpenAI Images 文生图和编辑分别走 [生成](/help/api?endpoint=image-generations) 和 [编辑](/help/api?endpoint=image-edits)；编辑的参数及上传形式可能不同。
- Gemini 原生图像模型用 [generateContent](/help/api?endpoint=gemini-generate-content)，同一响应可能含文本和图片部件。

## 先测试，后批量

先发一张低成本样例。尺寸、格式、是否可带参考图与价格以万川模型与当前计价为准；不要把一个模型的选项照搬到另一个模型。请求已返回但图片未保存时，先查结果与使用记录，不要立即重复发起。
`,
  },
  {
    slug: 'gemini-images',
    title: 'Gemini 原生图像模型',
    description: '确认模型具备图像输出能力，检查原生响应里的图片部件。',
    body: `
## 请求格式

从 [模型广场](/pricing) 选择支持 Gemini 生图的模型，记录对应原生 ID。用 [generateContent 示例](/help/api?endpoint=gemini-generate-content)，以站点根地址和 /v1beta 路径调用，配置 x-goog-api-key。图像输出选项按模型支持设置，不对仅文本模型要求图片返回。

## 读取并保存

逐个检查候选输出中的文本和内嵌图片部件；如含 MIME 类型和 Base64 数据，按实际类型保存。响应被过滤、只含文字或不支持所选尺寸时，先看结果状态和模型能力，不把它当作网络错误无限重试。
`,
  },
  {
    slug: 'openai-images',
    title: 'OpenAI Images 兼容接入',
    description: '分别处理生成、编辑和实际返回格式。',
    body: `
## 生成一张图片

以 \`{{BASE_URL}}/v1\` 为基础地址，使用支持 [images/generations](/help/api?endpoint=image-generations) 的模型。先用最少参数：模型和文字提示；如需固定比例或尺寸，先核对该模型允许的取值。

## 传入待编辑素材

改图走 [images/edits](/help/api?endpoint=image-edits)。例如支持文件上传的模型应按接口说明传 multipart 文件，不把文件路径直接当 JSON 图片内容。处理 URL 和 Base64 返回时按响应实际字段分支，注意链接可能失效，也不要把客户素材发给未授权的站点。
`,
  },
  {
    slug: 'faq',
    title: '万川 API 常见问题',
    description: '从模型找不到、协议不匹配、鉴权失败到扣费查看的排查顺序。',
    body: `
## 先确定问题发生在哪一层

1. [模型列表](/help/api?endpoint=models-list) 报 401：核对 Key 是否有效、鉴权头、客户端是否读了正确环境变量。
2. 模型列表为空或没有目标模型：检查 Key 的分组、模型限制和当前模型状态。
3. 列表能看到，实际调用失败：核对模型是否支持该客户端协议，检查用量/限流状态和上游错误。
4. 只看到账户余额变化：到 [使用日志](/usage-logs) 找请求与对应的模型，再看 [钱包](/wallet) 和 [账单说明](/help/guides/billing)。

## 按工具继续排查

查看 [Claude Code](/help/guides/faq-claude-code)、[Codex](/help/guides/faq-codex)、[Gemini](/help/guides/faq-gemini) 和 [缓存与代理排查](/help/guides/cli-cache-fix)。提交工单时附上请求 ID、时间、模型和状态码，不附 Key 或客户请求正文。
`,
  },
  {
    slug: 'faq-claude-code',
    title: 'Claude Code 常见问题',
    description: '排查登录冲突、请求地址与模型能力。',
    body: `
## 用的是万川还是官方登录？

从实际运行 CLI 的终端检查当前配置来源。万川用站点根地址和支持 Anthropic Messages 的 Key；如果终端环境变量与本地配置冲突，先备份，再确认当前会话用哪一份。不要输出变量的真实值。

## “模型不可用”或工具报错怎么办？

用同一把 Key 检查 [模型列表](/help/api?endpoint=models-list)，再核对 [Messages](/help/api?endpoint=claude-messages)。只在文本成功而工具失败时，再检查模型的工具能力和消息历史，不要直接删除原会话备份。
`,
  },
  {
    slug: 'faq-codex',
    title: 'Codex 常见问题',
    description: '排查 Responses 找不到、重复路径和桌面环境差异。',
    body: `
## 端点错了还是配置被覆盖？

Codex 的提供商基础地址通常带 /v1，客户端会补 /responses。不要输入整条请求路径。若提示模型不存在，核对完整 ID、Key 可用分组和 Responses 支持。

## App 与 CLI 为什么结果不同？

检查两者是否用同一个提供商配置与凭据。用新的临时会话重试短文本，区分原会话数据不兼容和基础服务不通；备份原配置后再改。参见 [Codex CLI 接入](/help/guides/codex-cli)。
`,
  },
  {
    slug: 'faq-gemini',
    title: 'Gemini 与 Gemini CLI 常见问题',
    description: '区分 Gemini 原生格式、OpenAI 兼容格式与原厂账号登录。',
    body: `
## 选择客户端实际支持的协议

万川原生接口从 /v1beta 开始，使用 x-goog-api-key。若客户端选择 OpenAI 兼容提供商，则使用 \`{{BASE_URL}}/v1\` 和对应 OpenAI 端点，不能用两个协议的配置混填。

## Gemini CLI 要求原厂登录怎么办？

万川 Key 不是 Google 账号凭据。仅当客户端版本明确允许自定义 Gemini 原生地址时才可使用万川根地址。对于不能自定义原生地址的版本，使用 [原生 API 参考](/help/api?endpoint=gemini-generate-content) 的 SDK / HTTP 示例，或改用支持外部地址的客户端。
`,
  },
  {
    slug: 'cli-cache-fix',
    title: 'CLI 缓存、代理与环境排查',
    description: '按地址、环境和会话逐一定位，不需要删除整个配置。',
    body: `
## 先做无凭据检查

检查实际运行客户端的终端能否访问万川站点。核对 IDE、系统代理和 WSL 的网络是否相同。保留证书校验；如果用了代理，不要把万川 Key 粘到不信任的测速网站。

## 逐层排除覆盖

核对客户端有效的 Base URL 与协议，再检查环境变量、本地项目配置和全局配置的优先级。重开客户端和新的会话；仍异常时先备份，再只清理客户端官方文档明确可重建的缓存。不删除 Key、会话历史或整份配置。

## 反馈给维护方

记录客户端版本、出错时间、HTTP 状态码和请求 ID。共享截图前遮住 API Key、Cookie 与个人信息。
`,
  },
  {
    slug: 'terms-and-policies',
    title: '万川条款与政策入口',
    description: '查看万川当前的服务条款和隐私政策原文。',
    body: `
## 以站内政策为准

- [用户协议](/user-agreement) 说明万川账号与服务的使用规则。
- [隐私政策](/privacy-policy) 说明个人信息相关事项。

本页只提供导航，不增删任何条款。涉及余额、发票、退款或充值的具体规则，以万川现行协议和控制台实际说明为准。需要人工核实时，可用 [工单](/support) 联系万川。
`,
  },
]
