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

import { loadChuanapiI18nOverride } from '..'
import en from '../locales/en.json'

describe('wallet and recharge translations', () => {
  test.each(['fr', 'ja', 'ru', 'vi'] as const)(
    '%s localizes the paid, bonus and total balances shown alongside the recharge offer',
    async (language) => {
      const bundle = await loadChuanapiI18nOverride(language)

      for (const key of [
        'Paid Balance',
        'Bonus Balance',
        'Total Balance',
        'Redeem Code',
        'No ledger records yet',
      ] as const) {
        expect(bundle?.translation[key], key).toBeTruthy()
        expect(bundle?.translation[key], key).not.toBe(en.translation[key])
      }
    }
  )

  test.each([
    ['ru', 'Сумма зачисления'],
    ['vi', 'Tổng tiền được cộng'],
  ] as const)(
    '%s describes the offer credit as the amount added, not the total wallet balance',
    async (language, expected) => {
      const bundle = await loadChuanapiI18nOverride(language)

      expect(bundle?.translation['Total credit']).toBe(expected)
      expect(bundle?.translation['Total credit']).not.toBe(
        bundle?.translation['Total Balance']
      )
    }
  )

  test.each(['en', 'zhCN', 'zhTW', 'fr', 'ja', 'ru', 'vi'] as const)(
    '%s supplies every extension label and retains its amount and count placeholders',
    async (language) => {
      const bundle = await loadChuanapiI18nOverride(language)

      for (const [key, value] of Object.entries(en.translation)) {
        expect(bundle?.translation[key], key).toBeTruthy()
        expect(
          bundle?.translation[key].match(/{{[^}]+}}/g)?.sort() ?? [],
          key
        ).toEqual(value.match(/{{[^}]+}}/g)?.sort() ?? [])
      }
    }
  )
})
