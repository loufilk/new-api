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
export type ModelSampleLanguage =
  | 'curl'
  | 'python'
  | 'typescript'
  | 'javascript'

type SampleContext = {
  baseUrl: string
  apiKeyEnv: string
  modelName: string
  endpointType: string
  endpointPath: string
  method?: string
}

export function modelEndpointAuth(
  type: string
): 'bearer' | 'anthropic' | 'gemini' | undefined {
  if (type === 'anthropic' || type === 'gemini') return type
  if (
    [
      'openai',
      'openai-response',
      'openai-response-compact',
      'embeddings',
      'image-generation',
      'jina-rerank',
      'openai-video',
      'audio-speech',
      'audio-transcription',
      'audio-translation',
      'realtime',
    ].includes(type)
  ) {
    return 'bearer'
  }
  return undefined
}

function sampleBody(
  type: string,
  model: string
): Record<string, unknown> | undefined {
  const text = 'Explain quantum entanglement in one paragraph.'
  const messages = [{ role: 'user', content: text }]
  switch (type) {
    case 'openai':
      return { model, messages }
    case 'openai-response':
      return { model, input: text }
    case 'openai-response-compact':
      return { model, input: messages }
    case 'anthropic':
      return { model, max_tokens: 1024, messages }
    case 'gemini':
      return { contents: [{ parts: [{ text }] }] }
    case 'embeddings':
      return { model, input: 'A short sentence to embed.' }
    case 'image-generation':
      return { model, prompt: 'A serene koi pond at sunset.', n: 1 }
    case 'openai-video':
      return { model, prompt: 'A slow aerial view of a mountain lake.' }
    case 'jina-rerank':
      return {
        model,
        query: 'What is idempotency?',
        documents: ['A duplicate-safe operation.', 'A rendering technique.'],
        top_n: 2,
      }
    default:
      return undefined
  }
}

// SDK methods choose their own URL. Use them only for their canonical routes;
// a site may advertise a different path for the very same protocol.
const sdkRoutes = new Map([
  [
    'openai',
    {
      path: '/v1/chat/completions',
      operation: 'chat.completions.create',
      result: 'choices[0].message.content',
    },
  ],
  [
    'openai-response',
    {
      path: '/v1/responses',
      operation: 'responses.create',
      result: 'output_text',
    },
  ],
  [
    'anthropic',
    {
      path: '/v1/messages',
      operation: 'messages.create',
      result: 'content[0].text',
    },
  ],
  [
    'embeddings',
    {
      path: '/v1/embeddings',
      operation: 'embeddings.create',
      result: 'data[0].embedding',
    },
  ],
  [
    'image-generation',
    {
      path: '/v1/images/generations',
      operation: 'images.generate',
      result: 'data',
    },
  ],
])

function canonicalSdkSample(
  lang: ModelSampleLanguage,
  ctx: SampleContext,
  endpointType: string,
  body: Record<string, unknown>
): string | undefined {
  const route = sdkRoutes.get(endpointType)
  if (
    !route ||
    route.path !== ctx.endpointPath ||
    (lang !== 'python' && lang !== 'typescript')
  ) {
    return undefined
  }
  const isAnthropic = endpointType === 'anthropic'
  const base = JSON.stringify(isAnthropic ? ctx.baseUrl : `${ctx.baseUrl}/v1`)
  if (lang === 'python') {
    // The five known SDK payloads contain only string, integer, list and
    // string-keyed dictionary literals, valid in both JSON and Python.
    const args = Object.entries(body).map(
      ([key, value]) => `    ${key}=${JSON.stringify(value)},`
    )
    return [
      'import os',
      isAnthropic
        ? 'from anthropic import Anthropic'
        : 'from openai import OpenAI',
      '',
      `client = ${isAnthropic ? 'Anthropic' : 'OpenAI'}(base_url=${base}, api_key=os.environ["${ctx.apiKeyEnv}"])`,
      '',
      `response = client.${route.operation}(`,
      ...args,
      ')',
      `print(response.${route.result})`,
    ].join('\n')
  }
  const sdk = isAnthropic ? 'Anthropic' : 'OpenAI'
  // Anthropic's SDK returns a union of content blocks; text isn't a property
  // of all blocks. Keep the example valid under strict TypeScript.
  const output = isAnthropic
    ? "for (const block of response.content) if (block.type === 'text') console.log(block.text)"
    : `console.log(response.${route.result})`
  return [
    `import ${sdk} from '${isAnthropic ? '@anthropic-ai/sdk' : 'openai'}'`,
    '',
    `const apiKey = process.env.${ctx.apiKeyEnv}`,
    `if (!apiKey) throw new Error('Set ${ctx.apiKeyEnv}')`,
    `const client = new ${sdk}({ baseURL: ${base}, apiKey })`,
    '',
    `const response = await client.${route.operation}(${JSON.stringify(body, null, 2)})`,
    output,
  ].join('\n')
}

// Samples target the catalog's actual method and path, including custom prefixes.
// Unknown protocols, incompatible methods, and non-JSON formats link to docs instead.
export function buildModelCodeSample(
  lang: ModelSampleLanguage,
  endpointType: string,
  ctx: SampleContext
): string | null {
  const body = sampleBody(endpointType, ctx.modelName)
  const auth = modelEndpointAuth(endpointType)
  if (!body || !auth || (ctx.method ?? 'POST').toUpperCase() !== 'POST') {
    return null
  }
  const url = ctx.baseUrl + ctx.endpointPath
  const env = ctx.apiKeyEnv
  if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(env)) return null
  const sdk = canonicalSdkSample(lang, ctx, endpointType, body)
  if (sdk) return sdk
  const header = {
    anthropic: 'x-api-key',
    gemini: 'x-goog-api-key',
    bearer: 'Authorization',
  }[auth]
  const version = auth === 'anthropic'
  const json = JSON.stringify(body, null, 2)
  if (lang === 'curl') {
    const quote = (value: string) => `'${value.replaceAll("'", "'\\''")}'`
    return [
      `curl -X POST ${quote(url)}`,
      `  -H "${header}: ${auth === 'bearer' ? 'Bearer ' : ''}$${env}"`,
      ...(version ? ["  -H 'anthropic-version: 2023-06-01'"] : []),
      "  -H 'Content-Type: application/json'",
      `  -d ${quote(json)}`,
    ].join(' \\\n')
  }
  if (lang === 'python') {
    return [
      'import json',
      'import os',
      'import requests',
      '',
      'response = requests.post(',
      `    ${JSON.stringify(url)},`,
      '    headers={',
      `        "${header}": ${
        auth === 'bearer' ? '"Bearer " + ' : ''
      }os.environ["${env}"],`,
      ...(version ? ['        "anthropic-version": "2023-06-01",'] : []),
      '        "Content-Type": "application/json",',
      '    },',
      `    json=json.loads(${JSON.stringify(json)}),`,
      '    timeout=120,',
      ')',
      'response.raise_for_status()',
      'print(response.json())',
    ].join('\n')
  }
  return [
    `const apiKey = process.env.${env}`,
    `if (!apiKey) throw new Error('Set ${env}')`,
    '',
    `const response = await fetch(${JSON.stringify(url)}, {`,
    "  method: 'POST',",
    '  headers: {',
    `    ${JSON.stringify(header)}: ${
      auth === 'bearer' ? "'Bearer ' + apiKey" : 'apiKey'
    },`,
    ...(version ? ["    'anthropic-version': '2023-06-01',"] : []),
    "    'Content-Type': 'application/json',",
    '  },',
    `  body: JSON.stringify(${json}),`,
    '})',
    'if (!response.ok) throw new Error(await response.text())',
    'console.log(await response.json())',
  ].join('\n')
}
