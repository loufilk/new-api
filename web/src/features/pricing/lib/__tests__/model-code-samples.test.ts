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
import { execFileSync } from 'node:child_process'
import { runInNewContext } from 'node:vm'

import { describe, expect, it, vi } from 'vitest'

import {
  buildModelCodeSample,
  type ModelSampleLanguage,
} from '../model-code-samples'

const ctx = {
  baseUrl: 'https://gateway.example',
  apiKeyEnv: 'API_TOKEN',
  modelName: 'MODEL_ID',
  endpointType: 'jina-rerank',
  endpointPath: '/v1/rerank',
}
describe('model endpoint code samples', () => {
  it.each<ModelSampleLanguage>(['curl', 'python', 'typescript', 'javascript'])(
    'uses query and documents rather than embedding input for rerank %s',
    (language) => {
      const code = buildModelCodeSample(language, 'jina-rerank', ctx)
      expect(code).toContain('query')
      expect(code).toContain('documents')
      expect(code).not.toContain('embeddings.create')
      expect(code).not.toContain('embedding.slice')
    }
  )
  it('does not silently offer chat completions for an unknown endpoint', () => {
    expect(buildModelCodeSample('curl', 'custom-protocol', ctx)).toBeNull()
  })
  it('uses video creation rather than chat fields for a video endpoint', () => {
    const code = buildModelCodeSample('javascript', 'openai-video', {
      ...ctx,
      endpointType: 'openai-video',
      endpointPath: '/v1/videos',
    })
    expect(code).toContain('/v1/videos')
    expect(code).toContain('prompt')
    expect(code).not.toContain('messages')
  })
})

describe('Gemini gateway examples', () => {
  const geminiContext = {
    ...ctx,
    endpointType: 'gemini',
    endpointPath: '/v1beta/models/MODEL_ID:generateContent',
  }
  const gatewayUrl =
    'https://gateway.example/v1beta/models/MODEL_ID:generateContent'

  it.each<ModelSampleLanguage>(['curl', 'python', 'typescript', 'javascript'])(
    'uses the gateway URL and header authentication in %s, not a direct Google SDK',
    (language) => {
      const code = buildModelCodeSample(language, 'gemini', geminiContext)
      expect(code).toContain(gatewayUrl)
      expect(code).toContain('x-goog-api-key')
      expect(code).not.toContain('?key=')
      expect(code).not.toContain('google.generativeai')
      expect(code).not.toContain('@google/generative-ai')
    }
  )

  it('reads the Python API key from the environment rather than a placeholder', () => {
    const code = buildModelCodeSample('python', 'gemini', geminiContext)
    expect(code).toContain('requests.post(')
    expect(code).toContain('os.environ["API_TOKEN"]')
    expect(code).toContain('response.raise_for_status()')
  })

  it('expands the configured environment variable in the curl header', () => {
    const code = buildModelCodeSample('curl', 'gemini', geminiContext)
    expect(code).not.toBeNull()
    // Replace only the network boundary; no HTTP request is sent.
    const args = execFileSync(
      'bash',
      ['-c', `curl() { printf '%s\\n' "$@"; }\n${code}`],
      { env: { API_TOKEN: 'fixture-key' }, encoding: 'utf8' }
    )
    expect(args).toContain(gatewayUrl)
    expect(args).toContain('x-goog-api-key: fixture-key')
    expect(args).not.toContain('$API_TOKEN')
  })

  it.each<ModelSampleLanguage>(['javascript', 'typescript'])(
    'sends %s requests to the gateway using an actual environment value',
    async (language) => {
      const code = buildModelCodeSample(language, 'gemini', geminiContext)
      expect(code).not.toBeNull()
      const fetch = vi.fn().mockResolvedValue({
        ok: true,
        json: async () => ({ candidates: [] }),
      })
      await runInNewContext(`(async () => { ${code} })()`, {
        fetch,
        process: { env: { API_TOKEN: 'fixture-key' } },
        console: { log: vi.fn() },
      })
      expect(fetch).toHaveBeenCalledWith(
        gatewayUrl,
        expect.objectContaining({
          method: 'POST',
          headers: {
            'x-goog-api-key': 'fixture-key',
            'Content-Type': 'application/json',
          },
        })
      )
      expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({
        contents: [
          {
            parts: [{ text: 'Explain quantum entanglement in one paragraph.' }],
          },
        ],
      })
    }
  )

  it('stops before sending a JavaScript request when the environment key is missing', async () => {
    const code = buildModelCodeSample('javascript', 'gemini', geminiContext)
    const fetch = vi.fn()
    await expect(
      runInNewContext(`(async () => { ${code} })()`, {
        fetch,
        process: { env: {} },
      })
    ).rejects.toThrow('Set API_TOKEN')
    expect(fetch).not.toHaveBeenCalled()
  })

  it('rejects HTTP errors instead of reading a successful Gemini result', async () => {
    const code = buildModelCodeSample('javascript', 'gemini', geminiContext)
    const json = vi.fn()
    await expect(
      runInNewContext(`(async () => { ${code} })()`, {
        fetch: vi.fn().mockResolvedValue({
          ok: false,
          text: async () => 'Request rejected',
          json,
        }),
        process: { env: { API_TOKEN: 'fixture-key' } },
      })
    ).rejects.toThrow('Request rejected')
    expect(json).not.toHaveBeenCalled()
  })
})
