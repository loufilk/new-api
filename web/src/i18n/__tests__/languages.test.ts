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
  normalizeInterfaceLanguage,
  toBackendLanguage,
  toIntlLocale,
} from '../languages'

describe('normalizeInterfaceLanguage', () => {
  test.each([
    ['zhCN', 'zhCN'],
    ['zhTW', 'zhTW'],
    ['en', 'en'],
    ['fr', 'fr'],
    ['ru', 'ru'],
    ['ja', 'ja'],
    ['vi', 'vi'],
  ])('preserves the saved interface language %s', (language, expected) => {
    expect(normalizeInterfaceLanguage(language)).toBe(expected)
  })

  test.each([
    ['zh', 'zhCN'],
    ['ZH_cn', 'zhCN'],
    [' zHcN ', 'zhCN'],
    ['zhtw', 'zhTW'],
    ['zh_hk', 'zhTW'],
    ['zh-MO', 'zhTW'],
    ['zh-Hant', 'zhTW'],
    ['zh-Hant-CN', 'zhTW'],
    ['zh-Hans-TW', 'zhCN'],
    ['zh-HK-u-nu-hanidec', 'zhTW'],
    ['en-GB', 'en'],
    ['fr-CA', 'fr'],
    ['ru_RU', 'ru'],
    ['ja-JP', 'ja'],
    [' vi-VN ', 'vi'],
  ])('restores the correct interface language for %s', (language, expected) => {
    expect(normalizeInterfaceLanguage(language)).toBe(expected)
  })

  test.each([
    undefined,
    null,
    '',
    ' ',
    'invalid_locale!',
    'zh-invalid!',
    'de-DE',
  ])('uses English for unsupported or invalid preference %s', (language) => {
    expect(normalizeInterfaceLanguage(language)).toBe('en')
  })
})

describe('toBackendLanguage', () => {
  test.each([
    ['zhCN', 'zh-CN'],
    ['zh-CN', 'zh-CN'],
    ['zhTW', 'zh-TW'],
    ['zh-Hant', 'zh-TW'],
    ['zh-Hans-TW', 'zh-CN'],
    ['zh-HK-u-nu-hanidec', 'zh-TW'],
    ['en', 'en'],
    ['fr', 'fr'],
    ['fr-CA', 'fr'],
    ['ru-RU', 'ru'],
    ['ja-JP', 'ja'],
    ['vi_VN', 'vi'],
  ])('maps %s to %s', (interfaceLanguage, expected) => {
    expect(toBackendLanguage(interfaceLanguage)).toBe(expected)
  })

  test('falls back to English when the interface language is unknown', () => {
    expect(toBackendLanguage('unknown')).toBe('en')
  })
})

describe('toIntlLocale', () => {
  test.each([
    ['zhCN', 'zh-CN'],
    ['zhTW', 'zh-TW'],
    ['en', 'en'],
    ['fr', 'fr'],
    ['ru', 'ru'],
    ['ja', 'ja'],
    ['vi', 'vi'],
    [' ZHCN ', 'zh-CN'],
    ['zhtw', 'zh-TW'],
    ['fr_CA', 'fr-CA'],
    ['zh-Hant-HK', 'zh-Hant-HK'],
  ])(
    'formats numbers and dates with a valid locale for %s',
    (language, expected) => {
      const locale = toIntlLocale(language)
      expect(locale).toBe(expected)
      expect(new Intl.NumberFormat(locale).format(1234.5)).toBe(
        new Intl.NumberFormat(expected).format(1234.5)
      )
      expect(
        new Intl.DateTimeFormat(locale).format(new Date('2026-11-07T00:00:00Z'))
      ).toBe(
        new Intl.DateTimeFormat(expected).format(
          new Date('2026-11-07T00:00:00Z')
        )
      )
    }
  )

  test.each([undefined, null, '', ' ', 'invalid_locale!', 'zh-invalid!'])(
    'uses the runtime locale for invalid input %s without throwing',
    (language) => {
      expect(toIntlLocale(language)).toBeUndefined()
      expect(() =>
        new Intl.NumberFormat(toIntlLocale(language)).format(1234.5)
      ).not.toThrow()
    }
  )
})
