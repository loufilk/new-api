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
import { API_BASE_URL } from '@/lib/api-url'

import type { EndpointDoc, GuideDoc } from '../content/types'

/**
 * Base URL used by docs samples and copyable snippets. It must point at the
 * API origin when the console is hosted separately, never at the web origin.
 */
export function getDocsOrigin(): string {
  if (API_BASE_URL) return API_BASE_URL
  if (typeof window === 'undefined') {
    return ''
  }
  return window.location.origin
}

/** Browser-visible origin for links back into the docs site itself. */
function getWebOrigin(): string {
  if (typeof window === 'undefined') return ''
  return window.location.origin
}

/** 把内容中的 {{BASE_URL}} / {{WS_BASE_URL}} 占位符替换为当前站点 origin */
export function applyBaseUrl(text: string, origin = getDocsOrigin()): string {
  return text
    .replaceAll('{{BASE_URL}}', origin)
    .replaceAll('{{WS_BASE_URL}}', origin.replace(/^http/, 'ws'))
}

/** 生成指南的完整 Markdown（用于「复制 Markdown」按钮） */
export function buildGuideMarkdown(
  guide: GuideDoc,
  origin = getDocsOrigin()
): string {
  const lines = [
    `# ${guide.title}`,
    '',
    `> ${guide.description}`,
    '',
    applyBaseUrl(guide.body, origin),
    '',
    `---`,
    '',
    `来源：${getWebOrigin()}/help/guides/${guide.slug}`,
  ]
  return lines.join('\n')
}

/** 生成端点文档的完整 Markdown（用于 API 参考页「复制 Markdown」按钮） */
export function buildEndpointMarkdown(
  endpoint: EndpointDoc,
  sampleCurl: string,
  origin = getDocsOrigin()
): string {
  const okResponse = endpoint.responses.find(
    (item) => item.status === 101 || (item.status >= 200 && item.status < 300)
  )
  const lines = [
    `# ${endpoint.method} ${endpoint.path} — ${endpoint.name}`,
    '',
    endpoint.description,
    '',
    `- Base URL: ${origin}`,
    `- 认证：${endpoint.authHeader ?? 'Authorization'}: ${!endpoint.authHeader || endpoint.authHeader === 'Authorization' ? 'Bearer ' : ''}<API_TOKEN>`,
    ...Object.entries(endpoint.extraHeaders ?? {}).map(
      ([name, value]) => `- ${name}: ${value}`
    ),
    `- 中继格式：${endpoint.relayFormat}`,
    `- 响应类型：${endpoint.responseType}`,
    '',
    '## 请求示例',
    '',
    '```bash',
    applyBaseUrl(sampleCurl, origin),
    '```',
  ]
  if (okResponse) {
    lines.push(
      '',
      `## 响应示例（${okResponse.status}）`,
      '',
      endpoint.responseKind ? '```text' : '```json',
      okResponse.body,
      '```'
    )
  }
  if (endpoint.notes.length > 0) {
    lines.push('', '## 备注', '')
    for (const note of endpoint.notes) {
      lines.push(`- ${note}`)
    }
  }
  lines.push('', '---', '', `来源：${getWebOrigin()}/help/api`)
  return lines.join('\n')
}
