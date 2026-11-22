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

import { IMAGE_REFERENCE_MAX_TOTAL_BYTES } from '../constants'
import { validateReferenceFiles } from '../lib/reference-validation'

function file(name: string, size: number, type: string) {
  return { name, size, type }
}

describe('validateReferenceFiles guards type, size, and count', () => {
  test('rejects models without reference support', () => {
    const error = validateReferenceFiles({
      files: [file('a.png', 100, 'image/png')],
      existingTotalBytes: 0,
      existingCount: 0,
      model: 'dall-e-3',
    })
    expect(error?.code).toBe('model_not_supported')
  })

  test('rejects non-image types', () => {
    const error = validateReferenceFiles({
      files: [file('notes.pdf', 100, 'application/pdf')],
      existingTotalBytes: 0,
      existingCount: 0,
      model: 'gpt-image-2',
    })
    expect(error?.code).toBe('unsupported_type')
  })

  test('rejects a png filename when the MIME type is a different binary', () => {
    const error = validateReferenceFiles({
      files: [file('spoofed.png', 100, 'application/octet-stream')],
      existingTotalBytes: 0,
      existingCount: 0,
      model: 'gpt-image-2',
    })
    expect(error?.code).toBe('unsupported_type')
  })

  test('trusts the filename extension only when the MIME type is empty', () => {
    const accepted = validateReferenceFiles({
      files: [
        file('a.png', 100, ''),
        file('b.jpg', 100, ''),
        file('c.jpeg', 100, ''),
        file('d.webp', 100, ''),
      ],
      existingTotalBytes: 0,
      existingCount: 0,
      model: 'gpt-image-2',
    })
    expect(accepted).toBeNull()
    const rejected = validateReferenceFiles({
      files: [file('notes.pdf', 100, '')],
      existingTotalBytes: 0,
      existingCount: 0,
      model: 'gpt-image-2',
    })
    expect(rejected?.code).toBe('unsupported_type')
  })

  test('accepts supported MIME types regardless of letter case', () => {
    const error = validateReferenceFiles({
      files: [file('photo.JPG', 100, 'IMAGE/JPEG')],
      existingTotalBytes: 0,
      existingCount: 0,
      model: 'gpt-image-2',
    })
    expect(error).toBeNull()
  })

  test('rejects totals above 15MB', () => {
    const error = validateReferenceFiles({
      files: [file('big.png', IMAGE_REFERENCE_MAX_TOTAL_BYTES, 'image/png')],
      existingTotalBytes: 1024,
      existingCount: 0,
      model: 'gpt-image-2',
    })
    expect(error?.code).toBe('total_size_exceeded')
  })

  test('rejects counts beyond the model reference limit', () => {
    const error = validateReferenceFiles({
      files: [file('a.png', 10, 'image/png'), file('b.png', 10, 'image/png')],
      existingTotalBytes: 0,
      existingCount: 0,
      model: 'dall-e-2',
    })
    expect(error?.code).toBe('too_many')
  })

  test('accepts a valid reference batch', () => {
    const error = validateReferenceFiles({
      files: [file('a.webp', 2048, 'image/webp')],
      existingTotalBytes: 0,
      existingCount: 0,
      model: 'gpt-image-2',
    })
    expect(error).toBeNull()
  })
})
