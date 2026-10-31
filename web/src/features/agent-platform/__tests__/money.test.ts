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

import { toIntlLocale } from '@/i18n/languages'

import {
  formatMinorMoney,
  minorMoneyInput,
  parseMinorMoney,
} from '../lib/money'

describe('original-currency money', () => {
  it('parses cents without binary rounding and retains int64 precision', () => {
    expect(parseMinorMoney('0.29', 2)).toBe('29')
    expect(parseMinorMoney('92233720368547758.07', 2)).toBe(
      '9223372036854775807'
    )
    expect(minorMoneyInput('9223372036854775807', 2)).toBe(
      '92233720368547758.07'
    )
  })
  it.each([
    '1.001',
    '1e3',
    '-1',
    '0',
    'NaN',
    'Infinity',
    '92233720368547758.08',
  ])('rejects invalid or unsafe monetary input %s', (value) => {
    expect(parseMinorMoney(value, 2)).toBeNull()
  })
  it('does not apply current exchange rates to original payment amounts', () => {
    expect(formatMinorMoney('12345', 'CNY', 2, 'en')).toBe('CNY 123.45')
    expect(formatMinorMoney('9223372036854775807', 'USD', 2, 'en')).toBe(
      'USD 92,233,720,368,547,758.07'
    )
    expect(formatMinorMoney('-1', 'USD', 2, 'en')).toBe('-USD 0.01')
  })
  it.each(['zhCN', 'zhTW', 'en', 'fr', 'ru', 'ja', 'vi', 'invalid_locale'])(
    'formats a fixed source amount for interface locale %s',
    (language) => {
      expect(
        formatMinorMoney('12345', 'USD', 2, toIntlLocale(language))
      ).toContain('USD')
    }
  )
  it('keeps integer currencies exact and rejects malformed server amounts', () => {
    expect(parseMinorMoney('123', 0)).toBe('123')
    expect(parseMinorMoney('123.0', 0)).toBeNull()
    expect(formatMinorMoney('123', 'JPY', 0, 'ja')).toContain('123')
    expect(formatMinorMoney('12e3', 'USD', 2, 'en')).toBe('—')
  })
})
