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
import { apiUrl } from '@/lib/api-url'

type PublishedLine = { id: string; label: string }
export type PublishedModel = {
  model_name: string
  public_line_ids: string[]
  supported_endpoint_types_by_public_line: Record<string, string[]>
}
export type PublishedCatalogue = {
  success: boolean
  public_lines: PublishedLine[]
  models: PublishedModel[]
}

// This is an anonymous, no-store overlay, not the cacheable pricing payload or
// the authenticated group API. Display IDs can never be sent to a relay API.
const privatePublicRead = {
  credentials: 'omit',
  redirect: 'error',
  cache: 'no-store',
} as const

export async function getWizardCatalogue(signal: AbortSignal) {
  const response = await fetch(apiUrl('/public-api/model-lines'), {
    ...privatePublicRead,
    signal,
  })
  if (!response.ok) throw new Error('Published model lines are unavailable')
  const data: PublishedCatalogue = await response.json()
  if (
    !data.success ||
    !Array.isArray(data.public_lines) ||
    !Array.isArray(data.models)
  )
    throw new Error('Published model lines are unavailable')
  return data
}

export async function getWizardAddresses(signal: AbortSignal) {
  const response = await fetch(apiUrl('/api/status'), {
    ...privatePublicRead,
    signal,
  })
  if (!response.ok) throw new Error('Connection addresses are unavailable')
  const { success, data } = await response.json()
  if (success !== true || !data || typeof data !== 'object') {
    throw new Error('Connection addresses are unavailable')
  }
  // Never use a persisted status snapshot for generated credential destinations.
  const configured: unknown[] =
    data.api_info_enabled !== false && Array.isArray(data.api_info)
      ? data.api_info
      : []
  const addresses = configured.flatMap((address) => {
    if (
      !address ||
      typeof address !== 'object' ||
      !('route' in address) ||
      typeof address.route !== 'string' ||
      !('url' in address) ||
      typeof address.url !== 'string'
    )
      return []
    const root = normalizeWizardAddress(address.url)
    return root ? [{ name: address.route, root }] : []
  })
  const fallback =
    typeof data.server_address === 'string'
      ? normalizeWizardAddress(data.server_address)
      : null
  return { addresses, fallback }
}

export const wizardClients = [
  {
    id: 'claude',
    name: 'Claude Code',
    protocols: ['anthropic'],
    guide: 'claude-code',
  },
  {
    id: 'codex',
    name: 'Codex CLI',
    protocols: ['openai-response'],
    guide: 'codex-cli',
  },
  {
    id: 'opencode',
    name: 'OpenCode',
    protocols: ['openai'],
    guide: 'opencode',
  },
  {
    id: 'cherry',
    name: 'Cherry Studio',
    protocols: ['openai'],
    guide: 'cherry-studio',
  },
  {
    id: 'workbuddy',
    name: 'WorkBuddy',
    protocols: ['openai'],
    guide: 'workbuddy',
  },
  {
    id: 'kimi',
    name: 'Kimi Code CLI',
    protocols: ['openai'],
    guide: 'kimi-code',
  },
  {
    id: 'openclaw',
    name: 'OpenClaw',
    protocols: ['openai'],
    guide: 'openclaw',
  },
  {
    id: 'hermes',
    name: 'Hermes Agent',
    protocols: ['openai'],
    guide: 'hermes-agent',
  },
  { id: 'aionui', name: 'AionUI', protocols: ['openai'], guide: 'aionui' },
  {
    id: 'cc-switch',
    name: 'CC Switch',
    protocols: ['anthropic', 'openai-response'],
    guide: 'cc-switch',
  },
  {
    id: 'api',
    name: 'HTTP API',
    protocols: [
      'anthropic',
      'openai-response',
      'openai',
      'gemini',
      'image-generation',
    ],
    guide: 'compatibility',
  },
] as const

export const wizardProtocols: Record<string, string> = {
  anthropic: 'Anthropic Messages',
  'openai-response': 'OpenAI Responses',
  openai: 'OpenAI Chat Completions',
  gemini: 'Gemini',
  'image-generation': 'OpenAI Images',
}

export type WizardOS = 'unix' | 'windows'
export type WizardClient = (typeof wizardClients)[number]

export function lineModelProtocols(
  model: PublishedModel | undefined,
  line: string
) {
  if (!model || !model.public_line_ids.includes(line)) return []
  return model.supported_endpoint_types_by_public_line[line] ?? []
}

export function normalizeWizardAddress(value: string) {
  try {
    const url = new URL(value)
    if (
      url.username ||
      url.password ||
      url.search ||
      url.hash ||
      (url.protocol !== 'https:' &&
        !(
          url.protocol === 'http:' &&
          ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname)
        ))
    )
      return null
    const path = url.pathname.replace(/\/+$/, '').replace(/\/(v1|v1beta)$/, '')
    return `${url.origin}${path}`
  } catch {
    return null
  }
}

function quote(value: string, os: WizardOS) {
  return os === 'windows'
    ? `'${value.replaceAll("'", "''")}'`
    : `'${value.replaceAll("'", "'\\''")}'`
}

export function buildWizardConfig(
  client: WizardClient,
  protocol: string,
  root: string,
  model: string,
  os: WizardOS
) {
  const baseV1 = `${root}/v1`
  const windows = os === 'windows'
  const modelJson = JSON.stringify(model)
  if (client.id === 'claude') {
    const prefix = windows ? '$env:' : 'export '
    const equals = windows ? ' = ' : '='
    return {
      label: windows ? 'PowerShell' : 'sh',
      setup: [
        `${prefix}ANTHROPIC_BASE_URL${equals}${quote(root, os)}`,
        `${prefix}ANTHROPIC_AUTH_TOKEN${equals}${windows ? '"$env:WANCHUAN_API_KEY"' : '"$WANCHUAN_API_KEY"'}`,
        `${prefix}ANTHROPIC_MODEL${equals}${quote(model, os)}`,
        'claude',
      ].join('\n'),
    }
  }
  if (client.id === 'codex') {
    return {
      label: windows
        ? '%USERPROFILE%\\.codex\\config.toml'
        : '~/.codex/config.toml',
      setup: [
        `model = ${modelJson}`,
        'model_provider = "wanchuan"',
        '',
        '[model_providers.wanchuan]',
        'name = "WanChuan"',
        `base_url = ${JSON.stringify(baseV1)}`,
        'env_key = "WANCHUAN_API_KEY"',
        'wire_api = "responses"',
      ].join('\n'),
    }
  }
  if (client.id === 'opencode') {
    return {
      label: 'opencode.json',
      setup: JSON.stringify(
        {
          $schema: 'https://opencode.ai/config.json',
          model: `wanchuan/${model}`,
          providers: {
            wanchuan: {
              name: 'WanChuan',
              env: ['WANCHUAN_API_KEY'],
              package: '@opencode/ai/providers/openai-compatible',
              settings: { baseURL: baseV1 },
              models: { [model]: { name: model } },
            },
          },
        },
        null,
        2
      ),
    }
  }
  if (client.id === 'cc-switch') {
    const base = protocol === 'anthropic' ? root : baseV1
    return {
      label: client.name,
      setup: `Protocol: ${wizardProtocols[protocol]}\nBase URL: ${base}\nModel: ${model}\nAPI Key: YOUR_WANCHUAN_API_KEY`,
    }
  }
  if (client.id !== 'api') {
    return {
      label: client.name,
      setup: `Provider: OpenAI-compatible\nBase URL: ${baseV1}\nModel: ${model}\nAPI Key: YOUR_WANCHUAN_API_KEY`,
    }
  }
  const method = windows ? 'curl.exe' : 'curl'
  const authToken = windows ? '$env:WANCHUAN_API_KEY' : '$WANCHUAN_API_KEY'
  const prompt = 'Hello'
  const group =
    protocol === 'anthropic'
      ? {
          path: '/v1/messages',
          headers: [
            `"x-api-key: ${authToken}"`,
            "'anthropic-version: 2023-06-01'",
          ],
          body: {
            model,
            max_tokens: 64,
            messages: [{ role: 'user', content: prompt }],
          },
        }
      : protocol === 'gemini'
        ? {
            path: `/v1beta/models/${encodeURIComponent(model)}:generateContent`,
            headers: [`"x-goog-api-key: ${authToken}"`],
            body: { contents: [{ parts: [{ text: prompt }] }] },
          }
        : {
            path:
              protocol === 'openai-response'
                ? '/v1/responses'
                : protocol === 'image-generation'
                  ? '/v1/images/generations'
                  : '/v1/chat/completions',
            headers: [`"Authorization: Bearer ${authToken}"`],
            body:
              protocol === 'openai-response'
                ? { model, input: prompt }
                : protocol === 'image-generation'
                  ? { model, prompt: 'A blue cube on a white background' }
                  : { model, messages: [{ role: 'user', content: prompt }] },
          }
  const headers = group.headers.map((header) => `-H ${header}`).join(' ')
  const command = `${method} ${quote(root + group.path, os)} ${headers} -H 'Content-Type: application/json' --data-binary '@-'`
  const body = JSON.stringify(group.body)
  return {
    label: windows ? 'PowerShell' : 'sh',
    setup: windows
      ? `${quote(body, os)} | ${command}`
      : `printf '%s' ${quote(body, os)} | ${command}`,
  }
}
