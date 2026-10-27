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
  isAbortError,
  normalizeImageResponse,
} from '../lib/request-builders'

describe('buildGenerationsBody constructs the JSON generations request', () => {
  test('includes model/prompt/n/response_format plus supported options', () => {
    const body = buildGenerationsBody({
      model: 'dall-e-3',
      prompt: 'a lake at sunrise',
      count: 4,
      size: '1792x1024',
      quality: 'hd',
      style: 'vivid',
    })
    expect(body).toEqual({
      model: 'dall-e-3',
      prompt: 'a lake at sunrise',
      n: 1,
      response_format: 'url',
      size: '1792x1024',
      quality: 'hd',
      style: 'vivid',
    })
  })

  test('omits quality/style/response_format when the model family does not support them', () => {
    const body = buildGenerationsBody({
      model: 'grok-beta',
      prompt: 'a city',
      count: 2,
      size: '',
      quality: '',
      style: '',
    })
    expect(body).toEqual({
      model: 'grok-beta',
      prompt: 'a city',
      n: 2,
    })
  })

  test('normalizes NaN counts to 1 instead of sending NaN upstream', () => {
    const body = buildGenerationsBody({
      model: 'gpt-image-2',
      prompt: 'a city',
      count: Number.NaN,
      size: 'auto',
      quality: 'medium',
      style: '',
    })
    expect(body.n).toBe(1)
  })

  test('normalizes infinite counts to 1 before clamping', () => {
    const capped = buildGenerationsBody({
      model: 'gpt-image-2',
      prompt: 'a city',
      count: Number.POSITIVE_INFINITY,
      size: 'auto',
      quality: 'medium',
      style: '',
    })
    expect(capped.n).toBe(1)
    const single = buildGenerationsBody({
      model: 'dall-e-3',
      prompt: 'a city',
      count: Number.POSITIVE_INFINITY,
      size: '1024x1024',
      quality: 'hd',
      style: 'vivid',
    })
    expect(single.n).toBe(1)
  })
})

describe('buildEditsFormData constructs the multipart edits request', () => {
  test('sends a single file as image and clamps count and references', () => {
    const first = new File(['a'], 'a.png', { type: 'image/png' })
    const second = new File(['b'], 'b.png', { type: 'image/png' })
    const form = buildEditsFormData({
      model: 'dall-e-2',
      prompt: 'edit this',
      count: 4,
      size: '1024x1024',
      images: [first, second],
    })
    expect(form.get('model')).toBe('dall-e-2')
    expect(form.get('prompt')).toBe('edit this')
    expect(form.get('n')).toBe('4')
    expect(form.get('size')).toBe('1024x1024')
    expect(form.get('response_format')).toBe('url')
    expect((form.get('image') as File).name).toBe('a.png')
  })

  test('sends multiple files as image[] for models with four references', () => {
    const files = [0, 1, 2].map(
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
    expect(form.getAll('image[]')).toHaveLength(3)
    expect(form.get('n')).toBe('2')
  })

  test('normalizes NaN and infinite counts to 1', () => {
    const nanForm = buildEditsFormData({
      model: 'dall-e-2',
      prompt: 'edit this',
      count: Number.NaN,
      size: '1024x1024',
      images: [new File(['a'], 'a.png', { type: 'image/png' })],
    })
    expect(nanForm.get('n')).toBe('1')
    const infiniteForm = buildEditsFormData({
      model: 'dall-e-2',
      prompt: 'edit this',
      count: Number.POSITIVE_INFINITY,
      size: '1024x1024',
      images: [new File(['a'], 'a.png', { type: 'image/png' })],
    })
    expect(infiniteForm.get('n')).toBe('1')
  })
})

describe('normalizeImageResponse handles url and b64_json payloads', () => {
  test.each([
    [
      { url: 'https://cdn.example/single.png' },
      'https://cdn.example/single.png',
      'url',
    ],
    [
      { b64_json: 'aGVsbG8=', mime_type: 'image/webp' },
      'data:image/webp;base64,aGVsbG8=',
      'b64',
    ],
  ])(
    'normalizes a single object-shaped image result: %j',
    (item, url, source) => {
      const images = normalizeImageResponse({
        data: { data: item },
        prompt: 'A lake',
        model: 'gpt-image-2',
        size: '1024x1024',
      })

      expect(images).toEqual([expect.objectContaining({ url, source })])
    }
  )

  test.each([{}, { url: 'javascript:alert(1)' }, { b64_json: 42 }])(
    'ignores an object result without a safe image payload: %j',
    (item) => {
      expect(
        normalizeImageResponse({
          data: { data: item },
          prompt: 'A lake',
          model: 'gpt-image-2',
          size: 'auto',
        })
      ).toEqual([])
    }
  )

  test('malformed items do not discard valid images in a mixed response', () => {
    expect(
      normalizeImageResponse({
        data: {
          data: [
            null,
            7,
            { b64_json: 42 },
            { url: 'https://cdn.example/valid.png' },
          ],
        },
        prompt: 'A lake',
        model: 'gpt-image-2',
        size: 'auto',
      })
    ).toEqual([
      expect.objectContaining({ url: 'https://cdn.example/valid.png' }),
    ])
  })

  test('keeps remote urls and converts base64 into data urls', () => {
    const images = normalizeImageResponse({
      data: {
        data: [
          { url: 'https://cdn.example/a.png' },
          { b64_json: 'aGVsbG8=' },
          {},
        ],
      },
      prompt: 'a lake',
      model: 'dall-e-3',
      size: '1024x1024',
    })
    expect(images).toHaveLength(2)
    expect(images[0].url).toBe('https://cdn.example/a.png')
    expect(images[0].source).toBe('url')
    expect(images[1].url).toBe('data:image/png;base64,aGVsbG8=')
    expect(images[1].source).toBe('b64')
  })

  test('returns empty for malformed payloads', () => {
    expect(
      normalizeImageResponse({ data: null, prompt: 'p', model: 'm', size: '' })
    ).toEqual([])
  })

  test('does not claim requested WxH values are actual image dimensions', () => {
    const images = normalizeImageResponse({
      data: {
        data: [{ url: 'https://cdn.example/a.png' }, { b64_json: 'aGVsbG8=' }],
      },
      prompt: 'a lake',
      model: 'gpt-image-2',
      size: '1792x1024',
    })
    expect(images).toHaveLength(2)
    expect(images[0].width).toBeUndefined()
    expect(images[0].height).toBeUndefined()
    expect(images[1].width).toBeUndefined()
    expect(images[1].height).toBeUndefined()
  })

  test('leaves width/height undefined for auto or empty sizes', () => {
    for (const size of ['auto', '']) {
      const images = normalizeImageResponse({
        data: { data: [{ url: 'https://cdn.example/a.png' }] },
        prompt: 'a lake',
        model: 'gpt-image-2',
        size,
      })
      expect(images).toHaveLength(1)
      expect(images[0].width).toBeUndefined()
      expect(images[0].height).toBeUndefined()
    }
  })
})

describe('isAbortError detects cancellation', () => {
  test('recognizes DOM abort and axios canceled errors', () => {
    expect(isAbortError(new DOMException('aborted', 'AbortError'))).toBe(true)
    expect(isAbortError({ code: 'ERR_CANCELED' })).toBe(true)
    expect(isAbortError(new Error('boom'))).toBe(false)
  })
})
