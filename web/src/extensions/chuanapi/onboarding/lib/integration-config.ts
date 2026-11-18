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
import {
  getModelEndpoints,
  type ModelEndpointMap,
} from '@/features/pricing/lib/model-endpoints'
import type { PricingModel } from '@/features/pricing/types'

export type IntegrationClient = 'api' | 'claude-code' | 'codex-cli'
export type IntegrationShell = 'posix' | 'powershell'

export function normalizeGatewayBaseUrl(value: string): string | null {
  try {
    const url = new URL(value)
    if (
      !['https:', 'http:'].includes(url.protocol) ||
      url.username ||
      url.password ||
      url.search ||
      url.hash
    ) {
      return null
    }
    const path = url.pathname
      .replace(/\/+$/, '')
      .replace(/\/v1(?:\/(?:chat\/completions|responses|messages))?$/, '')
    return `${url.origin}${path}`
  } catch {
    return null
  }
}

export function getIntegrationClients(
  model: PricingModel,
  endpointMap: ModelEndpointMap
): IntegrationClient[] {
  const types = new Set(
    getModelEndpoints(model, endpointMap).map((endpoint) => endpoint.type)
  )
  return [
    'api',
    ...(types.has('anthropic') ? ['claude-code' as const] : []),
    ...(types.has('openai-response') ? ['codex-cli' as const] : []),
  ]
}

export function getClientEndpointType(
  client: IntegrationClient,
  preferredType: string
): string {
  if (client === 'claude-code') return 'anthropic'
  if (client === 'codex-cli') return 'openai-response'
  return preferredType
}

function quoteShell(value: string, shell: IntegrationShell): string {
  return shell === 'powershell'
    ? `'${value.replaceAll("'", "''")}'`
    : `'${value.replaceAll("'", "'\"'\"'")}'`
}

export function buildEnvironmentConfig(shell: IntegrationShell): string {
  return shell === 'powershell'
    ? "$env:NEW_API_KEY = '<YOUR_API_KEY>'"
    : "export NEW_API_KEY='<YOUR_API_KEY>'"
}

export function buildClientConfig(
  client: IntegrationClient,
  shell: IntegrationShell,
  baseUrl: string,
  modelName: string
): { code: string; filename: string } | null {
  const base = normalizeGatewayBaseUrl(baseUrl)
  if (!base) return null
  if (client === 'codex-cli') {
    return {
      filename: 'config.toml',
      code: [
        `model = ${JSON.stringify(modelName)}`,
        'model_provider = "newapi"',
        '',
        '[model_providers.newapi]',
        'name = "New API"',
        `base_url = ${JSON.stringify(`${base}/v1`)}`,
        'env_key = "NEW_API_KEY"',
        'wire_api = "responses"',
      ].join('\n'),
    }
  }
  if (client === 'claude-code') {
    return {
      filename: shell === 'powershell' ? 'claude-env.ps1' : 'claude-env.sh',
      code:
        shell === 'powershell'
          ? [
              `$env:ANTHROPIC_BASE_URL = ${quoteShell(base, shell)}`,
              '$env:ANTHROPIC_AUTH_TOKEN = $env:NEW_API_KEY',
              `$env:ANTHROPIC_MODEL = ${quoteShell(modelName, shell)}`,
              'claude',
            ].join('\n')
          : [
              `export ANTHROPIC_BASE_URL=${quoteShell(base, shell)}`,
              'export ANTHROPIC_AUTH_TOKEN="$NEW_API_KEY"',
              `export ANTHROPIC_MODEL=${quoteShell(modelName, shell)}`,
              'claude',
            ].join('\n'),
    }
  }
  return null
}

export function buildVerificationRequest(
  shell: IntegrationShell,
  baseUrl: string,
  model: PricingModel,
  endpointMap: ModelEndpointMap,
  endpointType: string
): string | null {
  const base = normalizeGatewayBaseUrl(baseUrl)
  const endpoint = getModelEndpoints(model, endpointMap).find(
    (item) => item.type === endpointType
  )
  if (!base || !endpoint || endpoint.method.toUpperCase() !== 'POST') {
    return null
  }
  const bodies: Record<string, unknown> = {
    openai: {
      model: model.model_name,
      messages: [{ role: 'user', content: 'Hello' }],
      max_tokens: 64,
    },
    'openai-response': {
      model: model.model_name,
      input: 'Hello',
      max_output_tokens: 64,
    },
    anthropic: {
      model: model.model_name,
      max_tokens: 64,
      messages: [{ role: 'user', content: 'Hello' }],
    },
    gemini: {
      contents: [{ parts: [{ text: 'Hello' }] }],
      generationConfig: { maxOutputTokens: 64 },
    },
    embeddings: { model: model.model_name, input: 'Hello' },
    'jina-rerank': {
      model: model.model_name,
      query: 'Hello',
      documents: ['Hello world'],
      top_n: 1,
    },
    'image-generation': {
      model: model.model_name,
      prompt: 'A small green leaf on a white background',
      n: 1,
    },
  }
  const body = bodies[endpointType]
  if (!body) return null
  const headers: Record<string, string> = {
    Authorization: 'Bearer $NEW_API_KEY',
  }
  if (endpointType === 'anthropic') headers['anthropic-version'] = '2023-06-01'
  if (endpointType === 'gemini') {
    delete headers.Authorization
    headers['x-goog-api-key'] = '$NEW_API_KEY'
  }
  const url = `${base}${endpoint.path}`
  const json = JSON.stringify(body, null, 2)
  if (shell === 'powershell') {
    const headerEntries = Object.entries(headers)
      .map(
        ([key, value]) =>
          `${quoteShell(key, shell)} = ${value.includes('$NEW_API_KEY') ? `"${value.replace('$NEW_API_KEY', '$env:NEW_API_KEY')}"` : quoteShell(value, shell)}`
      )
      .join('; ')
    return [
      `Invoke-RestMethod -Method Post -Uri ${quoteShell(url, shell)} \``,
      `  -Headers @{ ${headerEntries} } \``,
      "  -ContentType 'application/json' `",
      `  -Body ${quoteShell(json, shell)}`,
    ].join('\n')
  }
  return [
    `curl --request POST ${quoteShell(url, shell)} \\`,
    ...Object.entries(headers).map(
      ([key, value]) => `  --header "${key}: ${value}" \\`
    ),
    "  --header 'Content-Type: application/json' \\",
    `  --data ${quoteShell(json, shell)}`,
  ].join('\n')
}
