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
// The extension composition must run before this module is evaluated: the
// marketing home and docs fallbacks are derived from the registered
// navigation extension at module load time.
import '@/extensions/bootstrap'
import { describe, expect, test } from 'vitest'

import {
  MARKETING_HOME_URL,
  QUICKSTART_DOCS_URL,
  resolvePublicDocsUrl,
} from '@/lib/public-site'

describe('public docs URL resolution', () => {
  test('blank or missing docs configuration falls back to the built-in docs', () => {
    expect(MARKETING_HOME_URL).toBe('https://chuanapi.com')
    expect(QUICKSTART_DOCS_URL).toBe('/help')

    expect(resolvePublicDocsUrl(undefined)).toBe('/help')
    expect(resolvePublicDocsUrl(null)).toBe('/help')
    expect(resolvePublicDocsUrl('')).toBe('/help')
    expect(resolvePublicDocsUrl('   ')).toBe('/help')
    expect(resolvePublicDocsUrl(42)).toBe('/help')
  })

  test.each([
    'https://chuanapi.com/docs/quickstart/',
    'https://chuanapi.com/docs/quickstart',
    'https://chuanapi.com/#quickstart',
    'https://chuanapi.com#quickstart',
  ])('the legacy docs entry %s resolves to the built-in docs', (entry) => {
    expect(resolvePublicDocsUrl(entry)).toBe('/help')
  })

  test('an explicit external docs URL is preserved', () => {
    expect(resolvePublicDocsUrl('https://docs.example.com/guide')).toBe(
      'https://docs.example.com/guide'
    )
  })

  test('the built-in help path resolves to itself', () => {
    expect(resolvePublicDocsUrl('/help')).toBe('/help')
  })
})
