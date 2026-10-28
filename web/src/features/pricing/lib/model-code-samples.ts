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
}

function buildChatSample(
  lang: ModelSampleLanguage,
  ctx: SampleContext
): string {
  const url = `${ctx.baseUrl}${ctx.endpointPath}`
  const isResponses = ctx.endpointType === 'openai-response'
  const userMessage = 'Explain quantum entanglement in one paragraph.'

  const bodyJson = isResponses
    ? JSON.stringify({ model: ctx.modelName, input: userMessage }, null, 2)
    : JSON.stringify(
        {
          model: ctx.modelName,
          messages: [{ role: 'user', content: userMessage }],
        },
        null,
        2
      )

  const fnCall = isResponses ? 'responses.create' : 'chat.completions.create'

  if (lang === 'curl') {
    return [
      `curl ${url} \\`,
      `  -H "Authorization: Bearer $${ctx.apiKeyEnv}" \\`,
      `  -H "Content-Type: application/json" \\`,
      `  -d '${bodyJson.replaceAll('\n', '\n     ')}'`,
    ].join('\n')
  }

  if (lang === 'python') {
    return [
      'from openai import OpenAI',
      '',
      'client = OpenAI(',
      `    base_url="${ctx.baseUrl}/v1",`,
      `    api_key="<YOUR_API_KEY>",`,
      ')',
      '',
      isResponses
        ? `response = client.${fnCall}(\n    model="${ctx.modelName}",\n    input="${userMessage}",\n)\n\nprint(response.output_text)`
        : `completion = client.${fnCall}(\n    model="${ctx.modelName}",\n    messages=[\n        {"role": "user", "content": "${userMessage}"}\n    ],\n)\n\nprint(completion.choices[0].message.content)`,
    ].join('\n')
  }

  if (lang === 'typescript') {
    return [
      `import OpenAI from 'openai'`,
      '',
      `const client = new OpenAI({`,
      `  baseURL: '${ctx.baseUrl}/v1',`,
      `  apiKey: process.env.${ctx.apiKeyEnv},`,
      `})`,
      '',
      isResponses
        ? `const response = await client.${fnCall}({\n  model: '${ctx.modelName}',\n  input: '${userMessage}',\n})\n\nconsole.log(response.output_text)`
        : `const completion = await client.${fnCall}({\n  model: '${ctx.modelName}',\n  messages: [{ role: 'user', content: '${userMessage}' }],\n})\n\nconsole.log(completion.choices[0].message.content)`,
    ].join('\n')
  }

  return [
    `const response = await fetch('${url}', {`,
    `  method: 'POST',`,
    `  headers: {`,
    `    Authorization: \`Bearer \${process.env.${ctx.apiKeyEnv}}\`,`,
    `    'Content-Type': 'application/json',`,
    `  },`,
    `  body: JSON.stringify(${bodyJson}),`,
    `})`,
    '',
    `const data = await response.json()`,
    `console.log(data)`,
  ].join('\n')
}

function buildAnthropicSample(
  lang: ModelSampleLanguage,
  ctx: SampleContext
): string {
  const url = `${ctx.baseUrl}${ctx.endpointPath}`
  const userMessage = 'Explain quantum entanglement in one paragraph.'

  if (lang === 'curl') {
    const body = JSON.stringify(
      {
        model: ctx.modelName,
        max_tokens: 1024,
        messages: [{ role: 'user', content: userMessage }],
      },
      null,
      2
    )
    return [
      `curl ${url} \\`,
      `  -H "x-api-key: $${ctx.apiKeyEnv}" \\`,
      `  -H "anthropic-version: 2023-06-01" \\`,
      `  -H "Content-Type: application/json" \\`,
      `  -d '${body.replaceAll('\n', '\n     ')}'`,
    ].join('\n')
  }
  if (lang === 'python') {
    return [
      'import anthropic',
      '',
      'client = anthropic.Anthropic(',
      `    base_url="${ctx.baseUrl}",`,
      `    api_key="<YOUR_API_KEY>",`,
      ')',
      '',
      `message = client.messages.create(`,
      `    model="${ctx.modelName}",`,
      `    max_tokens=1024,`,
      `    messages=[{"role": "user", "content": "${userMessage}"}],`,
      ')',
      '',
      'print(message.content[0].text)',
    ].join('\n')
  }
  if (lang === 'typescript') {
    return [
      `import Anthropic from '@anthropic-ai/sdk'`,
      '',
      `const client = new Anthropic({`,
      `  baseURL: '${ctx.baseUrl}',`,
      `  apiKey: process.env.${ctx.apiKeyEnv},`,
      `})`,
      '',
      `const message = await client.messages.create({`,
      `  model: '${ctx.modelName}',`,
      `  max_tokens: 1024,`,
      `  messages: [{ role: 'user', content: '${userMessage}' }],`,
      `})`,
      '',
      `console.log(message.content[0].text)`,
    ].join('\n')
  }
  return [
    `const response = await fetch('${url}', {`,
    `  method: 'POST',`,
    `  headers: {`,
    `    'x-api-key': process.env.${ctx.apiKeyEnv},`,
    `    'anthropic-version': '2023-06-01',`,
    `    'Content-Type': 'application/json',`,
    `  },`,
    `  body: JSON.stringify({`,
    `    model: '${ctx.modelName}',`,
    `    max_tokens: 1024,`,
    `    messages: [{ role: 'user', content: '${userMessage}' }],`,
    `  }),`,
    `})`,
    '',
    `const data = await response.json()`,
    `console.log(data.content[0].text)`,
  ].join('\n')
}

function buildGeminiSample(
  lang: ModelSampleLanguage,
  ctx: SampleContext
): string {
  const url = `${ctx.baseUrl}${ctx.endpointPath}`
  const body = JSON.stringify(
    {
      contents: [
        { parts: [{ text: 'Explain quantum entanglement in one paragraph.' }] },
      ],
    },
    null,
    2
  )

  // Use the gateway's native HTTP route in every language. Default Google SDK
  // clients target Google directly, and credentials must not be put in the URL.
  if (lang === 'curl') {
    return [
      `curl '${url.replaceAll("'", "'\\''")}' \\`,
      `  -H "x-goog-api-key: $${ctx.apiKeyEnv}" \\`,
      `  -H 'Content-Type: application/json' \\`,
      `  -d '${body.replaceAll('\n', '\n     ')}'`,
    ].join('\n')
  }
  if (lang === 'python') {
    return [
      'import os',
      'import requests',
      '',
      'response = requests.post(',
      `    ${JSON.stringify(url)},`,
      '    headers={',
      `        "x-goog-api-key": os.environ["${ctx.apiKeyEnv}"],`,
      '        "Content-Type": "application/json",',
      '    },',
      `    json=${body},`,
      '    timeout=120,',
      ')',
      'response.raise_for_status()',
      'print(response.json())',
    ].join('\n')
  }
  return [
    `const apiKey = process.env.${ctx.apiKeyEnv}`,
    `if (!apiKey) throw new Error('Set ${ctx.apiKeyEnv}')`,
    '',
    `const response = await fetch(${JSON.stringify(url)}, {`,
    `  method: 'POST',`,
    `  headers: {`,
    `    'x-goog-api-key': apiKey,`,
    `    'Content-Type': 'application/json',`,
    `  },`,
    `  body: JSON.stringify(${body}),`,
    `})`,
    '',
    `if (!response.ok) throw new Error(await response.text())`,
    `console.log(await response.json())`,
  ].join('\n')
}

function buildEmbeddingSample(
  lang: ModelSampleLanguage,
  ctx: SampleContext
): string {
  const url = `${ctx.baseUrl}${ctx.endpointPath}`
  const text = 'The food was delicious and the waiter…'

  if (lang === 'curl') {
    const body = JSON.stringify({ model: ctx.modelName, input: text }, null, 2)
    return [
      `curl ${url} \\`,
      `  -H "Authorization: Bearer $${ctx.apiKeyEnv}" \\`,
      `  -H "Content-Type: application/json" \\`,
      `  -d '${body.replaceAll('\n', '\n     ')}'`,
    ].join('\n')
  }
  if (lang === 'python') {
    return [
      'from openai import OpenAI',
      '',
      `client = OpenAI(base_url="${ctx.baseUrl}/v1", api_key="<YOUR_API_KEY>")`,
      '',
      'response = client.embeddings.create(',
      `    model="${ctx.modelName}",`,
      `    input="${text}",`,
      ')',
      '',
      'print(response.data[0].embedding[:8])',
    ].join('\n')
  }
  if (lang === 'typescript') {
    return [
      `import OpenAI from 'openai'`,
      '',
      `const client = new OpenAI({`,
      `  baseURL: '${ctx.baseUrl}/v1',`,
      `  apiKey: process.env.${ctx.apiKeyEnv},`,
      `})`,
      '',
      `const response = await client.embeddings.create({`,
      `  model: '${ctx.modelName}',`,
      `  input: '${text}',`,
      `})`,
      '',
      `console.log(response.data[0].embedding.slice(0, 8))`,
    ].join('\n')
  }
  return [
    `const response = await fetch('${url}', {`,
    `  method: 'POST',`,
    `  headers: {`,
    `    Authorization: \`Bearer \${process.env.${ctx.apiKeyEnv}}\`,`,
    `    'Content-Type': 'application/json',`,
    `  },`,
    `  body: JSON.stringify({`,
    `    model: '${ctx.modelName}',`,
    `    input: '${text}',`,
    `  }),`,
    `})`,
    '',
    `const data = await response.json()`,
    `console.log(data.data[0].embedding.slice(0, 8))`,
  ].join('\n')
}

function buildImageSample(
  lang: ModelSampleLanguage,
  ctx: SampleContext
): string {
  const url = `${ctx.baseUrl}${ctx.endpointPath}`
  const prompt = 'A serene koi pond at sunset, ukiyo-e style.'

  if (lang === 'curl') {
    const body = JSON.stringify({ model: ctx.modelName, prompt, n: 1 }, null, 2)
    return [
      `curl ${url} \\`,
      `  -H "Authorization: Bearer $${ctx.apiKeyEnv}" \\`,
      `  -H "Content-Type: application/json" \\`,
      `  -d '${body.replaceAll('\n', '\n     ')}'`,
    ].join('\n')
  }
  if (lang === 'python') {
    return [
      'from openai import OpenAI',
      '',
      `client = OpenAI(base_url="${ctx.baseUrl}/v1", api_key="<YOUR_API_KEY>")`,
      '',
      'response = client.images.generate(',
      `    model="${ctx.modelName}",`,
      `    prompt="${prompt}",`,
      `    n=1,`,
      ')',
      '',
      'print(response.data)',
    ].join('\n')
  }
  if (lang === 'typescript') {
    return [
      `import OpenAI from 'openai'`,
      '',
      `const client = new OpenAI({`,
      `  baseURL: '${ctx.baseUrl}/v1',`,
      `  apiKey: process.env.${ctx.apiKeyEnv},`,
      `})`,
      '',
      `const response = await client.images.generate({`,
      `  model: '${ctx.modelName}',`,
      `  prompt: '${prompt}',`,
      `  n: 1,`,
      `})`,
      '',
      `console.log(response.data)`,
    ].join('\n')
  }
  return [
    `const response = await fetch('${url}', {`,
    `  method: 'POST',`,
    `  headers: {`,
    `    Authorization: \`Bearer \${process.env.${ctx.apiKeyEnv}}\`,`,
    `    'Content-Type': 'application/json',`,
    `  },`,
    `  body: JSON.stringify({`,
    `    model: '${ctx.modelName}',`,
    `    prompt: '${prompt}',`,
    `    n: 1,`,
    `  }),`,
    `})`,
    '',
    `const data = await response.json()`,
    `console.log(data.data)`,
  ].join('\n')
}

export function buildModelCodeSample(
  lang: ModelSampleLanguage,
  endpointType: string,
  ctx: SampleContext
): string | null {
  if (endpointType === 'anthropic') return buildAnthropicSample(lang, ctx)
  if (endpointType === 'gemini') return buildGeminiSample(lang, ctx)
  if (endpointType === 'embeddings') {
    return buildEmbeddingSample(lang, ctx)
  }
  if (endpointType === 'image-generation') return buildImageSample(lang, ctx)
  if (endpointType === 'openai' || endpointType === 'openai-response') {
    return buildChatSample(lang, ctx)
  }
  if (endpointType === 'jina-rerank') {
    return buildStructuredSample(lang, ctx, {
      model: ctx.modelName,
      query: 'What is idempotency?',
      documents: ['A duplicate-safe operation.', 'A rendering technique.'],
      top_n: 2,
    })
  }
  if (endpointType === 'openai-video') {
    return buildStructuredSample(lang, ctx, {
      model: ctx.modelName,
      prompt: 'A slow aerial view of a mountain lake.',
    })
  }
  return null
}

function buildStructuredSample(
  lang: ModelSampleLanguage,
  ctx: SampleContext,
  body: Record<string, unknown>
): string {
  const url = `${ctx.baseUrl}${ctx.endpointPath}`
  const json = JSON.stringify(body, null, 2)
  if (lang === 'curl') {
    return [
      `curl -X POST "${url}" \\`,
      `  -H "Authorization: Bearer $${ctx.apiKeyEnv}" \\`,
      `  -H "Content-Type: application/json" \\`,
      `  -d '${json}'`,
    ].join('\n')
  }
  if (lang === 'python') {
    return `import json
import requests

response = requests.post(
    "${url}",
    headers={"Authorization": "Bearer <YOUR_API_KEY>"},
    json=json.loads(${JSON.stringify(json)}),
    timeout=120,
)
response.raise_for_status()
print(response.json())`
  }
  return `const response = await fetch('${url}', {
  method: 'POST',
  headers: {
    Authorization: \`Bearer \${process.env.${ctx.apiKeyEnv}}\`,
    'Content-Type': 'application/json',
  },
  body: JSON.stringify(${json}),
})
if (!response.ok) throw new Error(await response.text())
console.log(await response.json())`
}
