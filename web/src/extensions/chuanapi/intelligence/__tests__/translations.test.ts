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

import en from '@/i18n/locales/en.json'
import fr from '@/i18n/locales/fr.json'
import ja from '@/i18n/locales/ja.json'
import ru from '@/i18n/locales/ru.json'
import vi from '@/i18n/locales/vi.json'
import zhTW from '@/i18n/locales/zh-TW.json'
import zh from '@/i18n/locales/zh.json'

import { modelTestErrorKeys, modelTestFailureKeys } from '../errors'

describe('pool, tests and referral translations', () => {
  test.each(Object.entries({ en, zh, zhTW, fr, ja, ru, vi }))(
    '%s includes every new label and preserves interpolation variables',
    (_locale, bundle) => {
      const entries = bundle.translation as Record<string, string>
      for (const [key, value] of Object.entries(en.translation).filter(
        ([key]) =>
          /^(pool|intelligence|affiliate)\./.test(key) ||
          key === 'Enable model performance tests' ||
          key.startsWith(
            'Disabled by default. Turning this off hides the test entry'
          ) ||
          key.startsWith(
            'Saved changes take effect without a restart. Your test history'
          ) ||
          Object.values(modelTestErrorKeys).includes(key) ||
          Object.values(modelTestFailureKeys).includes(key)
      )) {
        expect(entries[key], key).toBeTruthy()
        expect(entries[key].match(/{{[^}]+}}/g)?.sort() ?? [], key).toEqual(
          value.match(/{{[^}]+}}/g)?.sort() ?? []
        )
      }
    }
  )
})
