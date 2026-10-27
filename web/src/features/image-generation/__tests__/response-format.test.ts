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
import { describe, expect, test } from 'vitest'

import {
  buildEditsFormData,
  buildGenerationsBody,
  normalizeImageResponse,
} from '../lib/request-builders'

test.each(['grok-imagine-image-2.0', 'grok-imagine-image-2.0-console'])(
  '%s requests inline image data instead of private asset URLs',
  (model) => {
    expect(
      buildGenerationsBody({
        model,
        prompt: 'A circle',
        count: 1,
        size: 'auto',
        quality: 'low',
        style: '',
      }).response_format
    ).toBe('b64_json')
  }
)

test.each([
  {
    b64_json: '/9j/4AAQSkZJRgABAQAA',
    mime_type: 'image/jpeg',
    mime: 'image/jpeg',
  },
  { b64_json: '/9j/4AAQSkZJRgABAQAA', mime: 'image/jpeg' },
  { b64_json: 'UklGRgAAAABXRUJQVlA4', mime: 'image/webp' },
])(
  'base64 images retain their actual MIME type: $mime',
  ({ mime, ...item }) => {
    const images = normalizeImageResponse({
      data: { data: [item] },
      model: 'grok-imagine-image-2.0',
      prompt: 'A circle',
      size: '',
    })
    expect(images[0].url).toBe(`data:${mime};base64,${item.b64_json}`)
  }
)

describe('generations omit response_format for GPT Image and keep it for DALL-E', () => {
  test('sends response_format url for dall-e-3 generations', () => {
    const body = buildGenerationsBody({
      model: 'dall-e-3',
      prompt: 'a lake',
      count: 1,
      size: '1024x1024',
      quality: 'standard',
      style: 'vivid',
    })
    expect(body.response_format).toBe('url')
  })

  test('sends response_format url for dall-e-2 generations', () => {
    const body = buildGenerationsBody({
      model: 'dall-e-2',
      prompt: 'a lake',
      count: 2,
      size: '512x512',
      quality: '',
      style: '',
    })
    expect(body.response_format).toBe('url')
  })

  test('omits response_format for gpt-image-1 generations defaulting to b64_json', () => {
    const body = buildGenerationsBody({
      model: 'gpt-image-1',
      prompt: 'a lake',
      count: 2,
      size: '1024x1024',
      quality: 'medium',
      style: '',
    })
    expect(body).not.toHaveProperty('response_format')
    expect(body.n).toBe(2)
  })

  test('omits response_format for gpt-image-2 generations', () => {
    const body = buildGenerationsBody({
      model: 'gpt-image-2',
      prompt: 'a city',
      count: 1,
      size: 'auto',
      quality: 'high',
      style: '',
    })
    expect(body).not.toHaveProperty('response_format')
  })
})

describe('edits omit response_format for GPT Image and keep it for DALL-E', () => {
  test('sends response_format url for dall-e-2 edits', () => {
    const file = new File(['a'], 'a.png', { type: 'image/png' })
    const form = buildEditsFormData({
      model: 'dall-e-2',
      prompt: 'edit this',
      count: 1,
      size: '1024x1024',
      images: [file],
    })
    expect(form.get('response_format')).toBe('url')
  })

  test('omits response_format for gpt-image-1 edits', () => {
    const file = new File(['a'], 'a.png', { type: 'image/png' })
    const form = buildEditsFormData({
      model: 'gpt-image-1',
      prompt: 'edit this',
      count: 1,
      size: '1024x1024',
      images: [file],
    })
    expect(form.get('response_format')).toBeNull()
    expect(form.get('model')).toBe('gpt-image-1')
    expect(form.get('n')).toBe('1')
  })

  test('omits response_format for gpt-image-2 edits with multiple references', () => {
    const files = [0, 1].map(
      (index) =>
        new File([String(index)], `${index}.png`, { type: 'image/png' })
    )
    const form = buildEditsFormData({
      model: 'gpt-image-2',
      prompt: 'blend these',
      count: 2,
      size: 'auto',
      images: files,
    })
    expect(form.get('response_format')).toBeNull()
    expect(form.getAll('image[]')).toHaveLength(2)
  })
})
