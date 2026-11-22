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
import type { EndpointDoc, SampleLanguage } from '../content/types'

export const sampleLanguages: readonly {
  id: SampleLanguage
  label: string
}[] = [
  { id: 'curl', label: 'cURL' },
  { id: 'javascript', label: 'JavaScript' },
  { id: 'python', label: 'Python' },
  { id: 'go', label: 'Go' },
]

function samplePath(path: string): string {
  return path
    .replaceAll('{model}', 'MODEL_ID')
    .replaceAll('{task_id}', 'TASK_ID')
    .replaceAll('{response_id}', 'RESPONSE_ID')
    .replaceAll('{artifact_key}', 'ARTIFACT_KEY')
}

function authValue(endpoint: EndpointDoc, token: string): string {
  return endpoint.authHeader === 'Authorization' || !endpoint.authHeader
    ? `Bearer ${token}`
    : token
}

function requestHeaders(
  endpoint: EndpointDoc,
  token: string
): readonly [string, string][] {
  const headers: [string, string][] = [
    [endpoint.authHeader ?? 'Authorization', authValue(endpoint, token)],
  ]
  for (const [name, value] of Object.entries(endpoint.extraHeaders ?? {})) {
    headers.push([name, value])
  }
  if (endpoint.requestKind === 'json') {
    headers.push(['Content-Type', 'application/json'])
  }
  return headers
}

function curlSample(endpoint: EndpointDoc, origin: string): string {
  if (endpoint.requestKind === 'websocket') {
    return [
      `curl --http1.1 --include --no-buffer "${origin}${samplePath(endpoint.path)}?model=MODEL_ID" \\`,
      '  -H "Authorization: Bearer $API_TOKEN" \\',
      '  -H "Connection: Upgrade" \\',
      '  -H "Upgrade: websocket" \\',
      '  -H "Sec-WebSocket-Version: 13" \\',
      '  -H "Sec-WebSocket-Key: $(openssl rand -base64 16)"',
    ].join('\n')
  }

  const lines = [
    `curl ${endpoint.method === 'HEAD' ? '--head' : `-X ${endpoint.method}`} "${origin}${samplePath(endpoint.path)}"`,
  ]
  for (const [name, value] of requestHeaders(endpoint, '$API_TOKEN')) {
    lines[lines.length - 1] += ' \\'
    lines.push(`  -H "${name}: ${value}"`)
  }
  if (endpoint.requestKind === 'json' && endpoint.requestBody) {
    lines[lines.length - 1] += ' \\'
    lines.push(`  -d '${endpoint.requestBody}'`)
  }
  if (endpoint.requestKind === 'multipart') {
    for (const field of endpoint.multipartFields ?? []) {
      lines[lines.length - 1] += ' \\'
      lines.push(`  -F "${field.name}=${field.file ? '@' : ''}${field.value}"`)
    }
  }
  if (endpoint.responseKind === 'binary') {
    lines[lines.length - 1] += ' \\'
    lines.push(`  --output ${endpoint.outputFilename ?? 'output.bin'}`)
  }
  return lines.join('\n')
}

function javascriptSample(endpoint: EndpointDoc, origin: string): string {
  if (endpoint.requestKind === 'websocket') {
    return `import WebSocket from 'ws'

const socket = new WebSocket(
  '${origin.replace(/^http/, 'ws')}${samplePath(endpoint.path)}?model=MODEL_ID',
  { headers: { Authorization: 'Bearer YOUR_API_TOKEN' } }
)

socket.on('open', () => {
  socket.send(JSON.stringify({ type: 'session.update', session: {} }))
})
socket.on('message', (data) => console.log(JSON.parse(data.toString())))`
  }

  const headers = Object.fromEntries(requestHeaders(endpoint, 'YOUR_API_TOKEN'))
  const options = [
    `  method: '${endpoint.method}',`,
    `  headers: ${JSON.stringify(headers, null, 2).replaceAll('\n', '\n  ')},`,
  ]
  let prelude = ''
  if (endpoint.requestKind === 'json' && endpoint.requestBody) {
    options.push(
      `  body: JSON.stringify(${endpoint.requestBody.replaceAll('\n', '\n  ')}),`
    )
  }
  if (endpoint.requestKind === 'multipart') {
    prelude = `import { openAsBlob } from 'node:fs'

const form = new FormData()
${(endpoint.multipartFields ?? [])
  .map((field) =>
    field.file
      ? `form.append('${field.name}', await openAsBlob('${field.value}'), '${field.value}')`
      : `form.append('${field.name}', '${field.value}')`
  )
  .join('\n')}

`
    options.push('  body: form,')
  }
  let readResponse = 'console.log(await response.json())'
  if (endpoint.responseKind === 'binary') {
    prelude = `import { writeFile } from 'node:fs/promises'\n${prelude}`
    readResponse = `await writeFile('${endpoint.outputFilename ?? 'output.bin'}', Buffer.from(await response.arrayBuffer()))`
  } else if (endpoint.method === 'HEAD' || endpoint.responseKind === 'none') {
    readResponse =
      'console.log(response.status, Object.fromEntries(response.headers))'
  }
  return `${prelude}const response = await fetch('${origin}${samplePath(endpoint.path)}', {
${options.join('\n')}
})

if (!response.ok) throw new Error(await response.text())
${readResponse}`
}

function pythonSample(endpoint: EndpointDoc, origin: string): string {
  if (endpoint.requestKind === 'websocket') {
    return `import asyncio
import json
import websockets

async def main():
    async with websockets.connect(
        "${origin.replace(/^http/, 'ws')}${samplePath(endpoint.path)}?model=MODEL_ID",
        additional_headers={"Authorization": "Bearer YOUR_API_TOKEN"},
    ) as socket:
        await socket.send(json.dumps({"type": "session.update", "session": {}}))
        print(await socket.recv())

asyncio.run(main())`
  }

  const headers = Object.fromEntries(requestHeaders(endpoint, 'YOUR_API_TOKEN'))
  const args = [
    `    "${endpoint.method}",`,
    `    "${origin}${samplePath(endpoint.path)}",`,
    `    headers=${JSON.stringify(headers, null, 2).replaceAll('\n', '\n    ')},`,
  ]
  let prelude = 'import requests\n\n'
  let cleanup = ''
  if (endpoint.requestKind === 'json' && endpoint.requestBody) {
    prelude = `import json\n${prelude}`
    args.push(`    json=json.loads(${JSON.stringify(endpoint.requestBody)}),`)
  }
  if (endpoint.requestKind === 'multipart') {
    const files = (endpoint.multipartFields ?? []).filter((field) => field.file)
    const fields = (endpoint.multipartFields ?? []).filter(
      (field) => !field.file
    )
    prelude += `${files
      .map((field) => `${field.name}_file = open("${field.value}", "rb")`)
      .join('\n')}\n\n`
    args.push(
      `    files={${files.map((field) => `"${field.name}": ${field.name}_file`).join(', ')}},`,
      `    data=${JSON.stringify(Object.fromEntries(fields.map((field) => [field.name, field.value])))},`
    )
    cleanup = `\n${files.map((field) => `${field.name}_file.close()`).join('\n')}`
  }
  args.push('    timeout=120,')
  let readResponse = 'print(response.json())'
  if (endpoint.responseKind === 'binary') {
    readResponse = `with open("${endpoint.outputFilename ?? 'output.bin'}", "wb") as output:\n    output.write(response.content)`
  } else if (endpoint.method === 'HEAD' || endpoint.responseKind === 'none') {
    readResponse = 'print(response.status_code, dict(response.headers))'
  }
  return `${prelude}response = requests.request(
${args.join('\n')}
)
response.raise_for_status()
${readResponse}${cleanup}`
}

function goSample(endpoint: EndpointDoc, origin: string): string {
  if (endpoint.requestKind === 'websocket') {
    return `package main

import (
  "fmt"
  "net/http"

  "github.com/gorilla/websocket"
)

func main() {
  headers := http.Header{"Authorization": []string{"Bearer YOUR_API_TOKEN"}}
  conn, _, err := websocket.DefaultDialer.Dial(
    "${origin.replace(/^http/, 'ws')}${samplePath(endpoint.path)}?model=MODEL_ID",
    headers,
  )
  if err != nil { panic(err) }
  defer conn.Close()

  if err := conn.WriteJSON(map[string]any{"type": "session.update", "session": map[string]any{}}); err != nil { panic(err) }
  _, message, err := conn.ReadMessage()
  if err != nil { panic(err) }
  fmt.Println(string(message))
}`
  }

  const headers: [string, string][] = [
    [
      endpoint.authHeader ?? 'Authorization',
      endpoint.authHeader === 'Authorization' || !endpoint.authHeader
        ? '"Bearer " + os.Getenv("API_TOKEN")'
        : 'os.Getenv("API_TOKEN")',
    ],
  ]
  for (const [name, value] of Object.entries(endpoint.extraHeaders ?? {})) {
    headers.push([name, JSON.stringify(value)])
  }
  if (endpoint.requestKind === 'json') {
    headers.push(['Content-Type', '"application/json"'])
  }
  if (endpoint.requestKind === 'multipart') {
    const fields = endpoint.multipartFields ?? []
    return `package main

import (
  "bytes"
  "fmt"
  "io"
  "mime/multipart"
  "net/http"
  "os"
  "path/filepath"
)

func main() {
  body := &bytes.Buffer{}
  writer := multipart.NewWriter(body)
${fields
  .map((field) => {
    if (!field.file) {
      return `  if err := writer.WriteField("${field.name}", "${field.value}"); err != nil { panic(err) }`
    }
    return `  file, err := os.Open("${field.value}")
  if err != nil { panic(err) }
  defer file.Close()
  part, err := writer.CreateFormFile("${field.name}", filepath.Base("${field.value}"))
  if err != nil { panic(err) }
  if _, err := io.Copy(part, file); err != nil { panic(err) }`
  })
  .join('\n')}
  if err := writer.Close(); err != nil { panic(err) }

  req, err := http.NewRequest("${endpoint.method}", "${origin}${samplePath(endpoint.path)}", body)
  if err != nil { panic(err) }
  req.Header.Set("Content-Type", writer.FormDataContentType())
${headers
  .map(([name, value]) => `  req.Header.Set("${name}", ${value})`)
  .join('\n')}

  response, err := http.DefaultClient.Do(req)
  if err != nil { panic(err) }
  defer response.Body.Close()
  result, err := io.ReadAll(response.Body)
  if err != nil { panic(err) }
  fmt.Println(string(result))
}`
  }

  let readResponse = 'fmt.Println(string(result))'
  if (endpoint.responseKind === 'binary') {
    readResponse = `if err := os.WriteFile("${endpoint.outputFilename ?? 'output.bin'}", result, 0600); err != nil { panic(err) }`
  } else if (endpoint.method === 'HEAD' || endpoint.responseKind === 'none') {
    readResponse = 'fmt.Println(response.Status, response.Header)'
  }
  const hasJSONBody = endpoint.requestKind === 'json' && endpoint.requestBody
  const body = hasJSONBody
    ? `bytes.NewBufferString(\`${endpoint.requestBody}\`)`
    : 'http.NoBody'
  const imports = [
    ...(hasJSONBody ? ['  "bytes"'] : []),
    ...(endpoint.responseKind === 'binary' ? [] : ['  "fmt"']),
    '  "io"',
    '  "net/http"',
    '  "os"',
  ].join('\n')
  return `package main

import (
${imports}
)

func main() {
  req, err := http.NewRequest(
    "${endpoint.method}",
    "${origin}${samplePath(endpoint.path)}",
    ${body},
  )
  if err != nil { panic(err) }
${headers
  .map(([name, value]) => `  req.Header.Set("${name}", ${value})`)
  .join('\n')}

  response, err := http.DefaultClient.Do(req)
  if err != nil { panic(err) }
  defer response.Body.Close()
  result, err := io.ReadAll(response.Body)
  if err != nil { panic(err) }
  if response.StatusCode >= 400 { panic(string(result)) }
  ${readResponse}
}`
}

export function generateEndpointSamples(
  endpoint: EndpointDoc,
  origin: string
): Record<SampleLanguage, string> {
  const generated: Record<SampleLanguage, string> = {
    curl: curlSample(endpoint, origin),
    javascript: javascriptSample(endpoint, origin),
    python: pythonSample(endpoint, origin),
    go: goSample(endpoint, origin),
  }
  return { ...generated, ...endpoint.sampleOverrides }
}
