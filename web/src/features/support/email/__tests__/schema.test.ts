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

import {
  isSupportEmailOrigin,
  supportEmailFormSchema,
  supportEmailRecipients,
  supportTicketSearch,
} from '../schema'

test('recipient input normalizes case and duplicates without accepting display-name or header injection syntax', () => {
  expect(
    supportEmailRecipients(
      'Admin@Example.com; second@example.com, admin@example.com'
    )
  ).toEqual(['admin@example.com', 'second@example.com'])
  for (const recipients of [
    'Admin <admin@example.com>',
    'admin@example.com\r\nBcc: other@example.com',
    'not-an-address',
    Array.from({ length: 6 }, (_, index) => `admin${index}@example.com`).join(
      ';'
    ),
  ]) {
    expect(
      supportEmailFormSchema.safeParse({
        enabled: false,
        recipients,
        siteOrigin: '',
      }).success
    ).toBe(false)
  }
})

test('enabling requires recipients and a trusted origin while an empty disabled configuration can be saved', () => {
  expect(
    supportEmailFormSchema.safeParse({
      enabled: false,
      recipients: '',
      siteOrigin: '',
    }).success
  ).toBe(true)
  expect(
    supportEmailFormSchema.safeParse({
      enabled: true,
      recipients: '',
      siteOrigin: '',
    }).success
  ).toBe(false)
  expect(
    supportEmailFormSchema.safeParse({
      enabled: true,
      recipients: 'support@example.com',
      siteOrigin: 'https://example.com',
    }).success
  ).toBe(true)
})

test.each([
  'http://example.com',
  'https://example.com/path',
  'https://example.com/',
  'https://example.com?x=1',
  'https://example.com#part',
  'https://user:password@example.com',
  '//example.com',
  'javascript:alert(1)',
  'https://*.example.com',
  'https://example.com\n',
])('unsafe or non-canonical site origin %s is rejected', (value) => {
  expect(isSupportEmailOrigin(value)).toBe(false)
})

test.each([
  0,
  -1,
  '1.2',
  '1e2',
  'https://evil.example',
  true,
  {},
  '9007199254740992',
  undefined,
])('invalid deep-link ticket %s does not select a ticket', (ticket) => {
  expect(supportTicketSearch({ ticket })).toEqual({})
})

test('valid deep links keep only a safe positive ticket ID and ignore redirect injection', () => {
  expect(
    supportTicketSearch({ ticket: '42', redirect: 'https://evil.example' })
  ).toEqual({ ticket: 42 })
  expect(supportTicketSearch({ ticket: 42 })).toEqual({ ticket: 42 })
})
