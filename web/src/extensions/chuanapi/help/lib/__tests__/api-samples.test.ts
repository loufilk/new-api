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

import { getEndpoint } from '../../content/endpoints'
import { generateEndpointSamples } from '../api-samples'
import { buildEndpointMarkdown } from '../docs-utils'

const origin = 'https://gateway.example'
describe('copyable API examples', () => {
  it('explains persisted image timeouts as queryable failures, not successful or retryable submissions', () => {
    for (const id of ['image-generations', 'image-edits']) {
      const response = getEndpoint(id).responses.find(
        (item) => item.status === 504
      )
      expect(response).toBeDefined()
      expect(JSON.parse(response?.body ?? '{}')).toMatchObject({
        error: { code: 'task_timeout', type: 'server_error' },
        task_id: 'TASK_ID',
        status_url: '/v1/tasks/TASK_ID',
        retryable: false,
      })
    }
  })
  it('decodes JSON bodies in Python instead of embedding JSON booleans as Python literals', () => {
    const sample = generateEndpointSamples(
      getEndpoint('chat-completions'),
      origin
    ).python
    expect(sample).toContain('import json')
    expect(sample).toContain('json=json.loads(')
  })
  it('writes speech as binary in every HTTP language instead of parsing JSON', () => {
    const samples = generateEndpointSamples(getEndpoint('audio-speech'), origin)
    expect(samples.curl).toContain('--output output.mp3')
    expect(samples.javascript).toContain('response.arrayBuffer()')
    expect(samples.javascript).not.toContain('response.json()')
    expect(samples.python).toContain('response.content')
    expect(samples.python).not.toContain('response.json()')
    expect(samples.go).toContain('os.WriteFile("output.mp3"')
  })
  it('exports the selected protocol authentication and fixed headers in Markdown', () => {
    const doc = buildEndpointMarkdown(
      getEndpoint('claude-messages'),
      '',
      origin
    )
    expect(doc).toContain('x-api-key: <API_TOKEN>')
    expect(doc).toContain('anthropic-version: 2023-06-01')
    expect(doc).not.toContain('Authorization: Bearer')
    expect(
      buildEndpointMarkdown(getEndpoint('gemini-generate-content'), '', origin)
    ).toContain('x-goog-api-key: <API_TOKEN>')
  })
  it('exports WebSocket upgrade status rather than omitting its successful response', () => {
    expect(
      buildEndpointMarkdown(getEndpoint('realtime'), '', origin)
    ).toContain('101')
  })
  it('provides reusable compact output rather than a fabricated compacted flag', () => {
    const result = JSON.parse(
      getEndpoint('responses-compact').responses[0].body
    )
    expect(result.output).toEqual(expect.any(Array))
    expect(result).not.toHaveProperty('compacted')
  })
  it('documents video creation, retrieval, binary content and HEAD without inventing chat payloads', () => {
    expect(getEndpoint('videos-create').path).toBe('/v1/videos')
    expect(getEndpoint('videos-retrieve').path).toBe('/v1/videos/{task_id}')
    expect(getEndpoint('videos-content').path).toBe(
      '/v1/videos/{task_id}/content'
    )
    expect(getEndpoint('videos-content-head').method).toBe('HEAD')
    const sample = generateEndpointSamples(
      getEndpoint('videos-content-head'),
      origin
    )
    expect(sample.javascript).not.toContain('response.json()')
    expect(sample.python).not.toContain('response.json()')
  })
})
