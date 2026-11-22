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
import { expect, test } from 'vitest'

import { SUPPORT_IMAGE_MAX_BYTES, supportImageError } from '../lib/attachments'
import { createSupportTicketSchema } from '../lib/schema'

const png = () => new File(['png'], 'synthetic.png', { type: 'image/png' })

test('image validation checks combined count and accepts no more than four', () => {
  expect(supportImageError([png(), png(), png(), png()])).toBeNull()
  expect(supportImageError([png()], 4)).toBe(
    'Attach at most 4 images per message.'
  )
})

test.each(['image/svg+xml', 'text/html', 'application/octet-stream', ''])(
  'image picker rejects unsupported declared media type %s',
  (type) => {
    expect(
      supportImageError([new File(['spoof'], 'looks-like.png', { type })])
    ).toBe('Only PNG, JPEG and WebP images are supported.')
  }
)

test('image picker rejects empty and oversize files; backend still decodes all accepted uploads', () => {
  expect(
    supportImageError([new File([], 'empty.png', { type: 'image/png' })])
  ).toBe('Each image must be nonempty and no larger than 5 MB.')
  expect(
    supportImageError([
      new File([new Uint8Array(SUPPORT_IMAGE_MAX_BYTES + 1)], 'large.png', {
        type: 'image/png',
      }),
    ])
  ).toBe('Each image must be nonempty and no larger than 5 MB.')
})

test('contact email is optional, trimmed and validated, and categories are an allowlist', () => {
  const base = {
    subject: 'Help',
    message: 'A description',
    category: 'other',
    contact_email: '',
  }
  expect(createSupportTicketSchema.parse(base).contact_email).toBe('')
  expect(
    createSupportTicketSchema.parse({
      ...base,
      contact_email: ' person@example.com ',
    }).contact_email
  ).toBe('person@example.com')
  expect(
    createSupportTicketSchema.safeParse({
      ...base,
      contact_email: 'not an email',
    }).success
  ).toBe(false)
  expect(
    createSupportTicketSchema.safeParse({ ...base, category: 'unauthorized' })
      .success
  ).toBe(false)
})
