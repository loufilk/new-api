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
  describeMagpieProvider,
  isMagpieEligibleKey,
  magpieImportUrl,
  supportsMagpieDesktop,
} from '../magpie-import'

test('binds the direct import to exactly the canonical gateway, with per-Key identity', () => {
  const provider = describeMagpieProvider(
    'https://API.example.test:443/mount/',
    7
  )
  expect(provider).not.toBeNull()
  if (!provider) return
  const key = 'sk-synthetic+&=?#'
  const href = magpieImportUrl(provider, key)
  expect(href).toMatch(/^magpie:\/\/import\?/)
  const link = new URL(href ?? '')
  expect(link.hash).toBe('')
  expect(Object.fromEntries(link.searchParams)).toEqual({
    id: expect.stringMatching(/^new-api-7-[a-f0-9]{24}$/),
    name: 'New API (Key 7)',
    chat: 'https://api.example.test/mount/v1',
    responses: 'https://api.example.test/mount/v1',
    anthropic: 'https://api.example.test/mount',
    gemini: 'https://api.example.test/mount/v1beta',
    key,
  })
  expect(describeMagpieProvider('https://api.example.test/mount', 7)?.id).toBe(
    provider.id
  )
  expect(
    describeMagpieProvider('https://api.example.test/mount', 8)?.id
  ).not.toBe(provider.id)
  expect(
    describeMagpieProvider('https://api.example.test/other', 7)?.id
  ).not.toBe(provider.id)
  expect(
    describeMagpieProvider('https://different.example.test/mount', 7)?.id
  ).not.toBe(provider.id)
})

test.each([
  '//public.example.test',
  'javascript:foo()',
  'https://',
  'https://another.example.test:65536',
  'http://public.example.test',
  'http://127.0.0.1.invalid',
  'https://name:pw@example.test',
  'https://@example.test',
  'https://localhost/api?key=example',
  'https://example.test#fragment',
  'https://example.test?',
  'https://example.test/prefix/../other',
  'https://example.test/prefix/%2fother',
  'https://example.test/prefix//other',
  'https://example.test/v1',
  'https://example.test/\tproxy',
])('refuses unsuitable endpoint %s', (base) => {
  expect(describeMagpieProvider(base, 7)).toBeNull()
})

test.each([
  'http://localhost:3000',
  'http://127.0.0.1:3000',
  'http://[::1]:3000',
])(
  'supports local development base %s without relaxing public HTTP',
  (base) => {
    expect(describeMagpieProvider(base, 7)?.chat).toBe(`${base}/v1`)
  }
)

test('rejects empty, invalid or oversized keys instead of emitting a link', () => {
  const provider = describeMagpieProvider('https://api.example.test', 7)
  expect(provider).not.toBeNull()
  if (!provider) return
  for (const key of [
    'sk-',
    'unprefixed',
    'sk-hidden\rdata',
    'sk-empty field',
    `sk-${'x'.repeat(4096)}`,
    `sk-${'é'.repeat(2048)}`,
  ]) {
    expect(magpieImportUrl(provider, key)).toBeNull()
  }
})

test('only offers import for enabled, unexpired persisted Keys', () => {
  const token = { id: 7, status: 1, expired_time: -1 }
  const now = 2_000
  expect(isMagpieEligibleKey(token, now)).toBe(true)
  expect(isMagpieEligibleKey({ ...token, status: 2 }, now)).toBe(false)
  expect(isMagpieEligibleKey({ ...token, expired_time: 2 }, now)).toBe(false)
  expect(isMagpieEligibleKey({ ...token, expired_time: 3 }, now)).toBe(true)
  expect(isMagpieEligibleKey({ ...token, id: 0 }, now)).toBe(false)
})

test.each([
  ['Linux x86_64', 'Linux', 0, true],
  ['Windows NT 10.0', 'Win32', 0, true],
  ['Macintosh', 'MacIntel', 0, true],
  ['Macintosh', 'MacIntel', 5, false],
  ['Android Mobile Linux', 'Linux armv8l', 5, false],
  ['iPhone', 'iPhone', 5, false],
  ['iPad', 'iPad', 5, false],
  ['X11; CrOS', 'Linux', 0, false],
  ['', '', 0, false],
])(
  'platform %s (%s, %d) gives local launch guidance only when supported',
  (userAgent, platform, maxTouchPoints, supported) => {
    expect(supportsMagpieDesktop({ userAgent, platform, maxTouchPoints })).toBe(
      supported
    )
  }
)
