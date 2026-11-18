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
import { describe, expect, it } from 'vitest'

import type { PricingModel } from '@/features/pricing/types'

import {
  buildClientConfig,
  buildEnvironmentConfig,
  buildVerificationRequest,
  getIntegrationClients,
  normalizeGatewayBaseUrl,
} from '../lib/integration-config'

const endpointMap = {
  openai: { path: '/v1/chat/completions', method: 'POST' },
  'openai-response': { path: '/v1/responses', method: 'POST' },
  anthropic: { path: '/v1/messages', method: 'POST' },
  gemini: { path: '/v1beta/models/{model}:generateContent', method: 'POST' },
}
function model(overrides: Partial<PricingModel> = {}): PricingModel {
  return {
    id: 1,
    model_name: 'gpt-4.1',
    quota_type: 0,
    model_ratio: 1,
    completion_ratio: 3,
    enable_groups: ['default'],
    supported_endpoint_types: ['openai', 'openai-response'],
    ...overrides,
  }
}

describe('integration configuration', () => {
  it('offers Codex only when the catalog declares a valid Responses endpoint', () => {
    expect(getIntegrationClients(model(), endpointMap)).toEqual([
      'api',
      'codex-cli',
    ])
    expect(
      getIntegrationClients(
        model({ supported_endpoint_types: ['openai'] }),
        endpointMap
      )
    ).toEqual(['api'])
    expect(
      getIntegrationClients(model(), {
        ...endpointMap,
        'openai-response': { path: '//untrusted.example/v1/responses' },
      })
    ).toEqual(['api'])
    expect(
      getIntegrationClients(
        model({ supported_endpoint_types: ['anthropic'] }),
        endpointMap
      )
    ).toEqual(['api', 'claude-code'])
  })

  it('normalizes configured gateway paths while preserving an installation prefix', () => {
    expect(
      normalizeGatewayBaseUrl(
        'https://gateway.example/proxy/v1/chat/completions/'
      )
    ).toBe('https://gateway.example/proxy')
    expect(normalizeGatewayBaseUrl('https://gateway.example/v1')).toBe(
      'https://gateway.example'
    )
    expect(normalizeGatewayBaseUrl('https://gateway.example')).toBe(
      'https://gateway.example'
    )
    for (const url of [
      'javascript:alert(1)',
      'https://user:password@gateway.example',
      'https://gateway.example?key=secret',
      'not a URL',
    ]) {
      expect(normalizeGatewayBaseUrl(url)).toBeNull()
    }
  })

  it('exports a Responses Codex config that references an environment variable', () => {
    const result = buildClientConfig(
      'codex-cli',
      'posix',
      'https://gateway.example/v1',
      'model"quoted'
    )
    expect(result?.filename).toBe('config.toml')
    expect(result?.code).toContain('wire_api = "responses"')
    expect(result?.code).toContain('base_url = "https://gateway.example/v1"')
    expect(result?.code).toContain('env_key = "NEW_API_KEY"')
    expect(result?.code).toContain('model = "model\\"quoted"')
    expect(result?.code).not.toContain('<YOUR_API_KEY>')
  })

  it('quotes model values as literals in both shell formats', () => {
    const name = "claude-'$(touch /tmp/unsafe)'"
    const posix = buildClientConfig(
      'claude-code',
      'posix',
      'https://gateway.example',
      name
    )?.code
    const powershell = buildClientConfig(
      'claude-code',
      'powershell',
      'https://gateway.example',
      name
    )?.code
    expect(posix).toContain(
      "export ANTHROPIC_MODEL='claude-'\"'\"'$(touch /tmp/unsafe)'\"'\"''"
    )
    expect(powershell).toContain(
      "$env:ANTHROPIC_MODEL = 'claude-''$(touch /tmp/unsafe)'''"
    )
    expect(buildEnvironmentConfig('powershell')).toBe(
      "$env:NEW_API_KEY = '<YOUR_API_KEY>'"
    )
  })

  it('generates protocol-specific requests and rejects unsupported protocols', () => {
    const responses = buildVerificationRequest(
      'posix',
      'https://gateway.example/v1',
      model(),
      endpointMap,
      'openai-response'
    )
    expect(responses).toContain("'https://gateway.example/v1/responses'")
    expect(responses).toContain('"input": "Hello"')
    expect(responses).not.toContain('"messages"')
    expect(
      buildVerificationRequest(
        'posix',
        'https://gateway.example',
        model(),
        endpointMap,
        'anthropic'
      )
    ).toBeNull()
    const gemini = buildVerificationRequest(
      'powershell',
      'https://gateway.example',
      model({ model_name: 'gemini-pro', supported_endpoint_types: ['gemini'] }),
      endpointMap,
      'gemini'
    )
    expect(gemini).toContain('gemini-pro:generateContent')
    expect(gemini).toContain('\'x-goog-api-key\' = "$env:NEW_API_KEY"')
    expect(gemini).not.toContain('Authorization')
  })
})
