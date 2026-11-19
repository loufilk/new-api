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

import { customerConfigSchema, estimateInvoiceFee } from '../policy'

const ordinary = {
  enabled: true,
  fee_enabled: true,
  fee_rate_bps: 100,
  tax_rate_bps: 600,
}
const special = {
  enabled: false,
  fee_enabled: false,
  fee_rate_bps: 1500,
  tax_rate_bps: 0,
}

test('a legacy unversioned config opens both types fee-free but an incomplete modern policy fails closed', () => {
  const legacy = customerConfigSchema.parse({ enabled: true })
  expect(legacy.version).toBeUndefined()
  for (const type of [legacy.ordinary, legacy.special]) {
    expect(type.enabled).toBe(true)
    expect(type.fee_enabled).toBe(false)
  }
  expect(
    customerConfigSchema.safeParse({ enabled: true, version: 1 }).success
  ).toBe(false)
  expect(
    customerConfigSchema.safeParse({ enabled: true, ordinary }).success
  ).toBe(false)
  expect(
    customerConfigSchema.safeParse({
      enabled: true,
      version: 2,
      ordinary: { ...ordinary, fee_rate_bps: -1 },
      special,
    }).success
  ).toBe(false)
})

test('fees round half-up exactly once and remain precise beyond the JS safe integer range', () => {
  expect(estimateInvoiceFee(49n, ordinary)).toBe(0n)
  expect(estimateInvoiceFee(50n, ordinary)).toBe(1n)
  expect(estimateInvoiceFee(9007199254740992950n, ordinary)).toBe(
    90071992547409930n
  )
  expect(estimateInvoiceFee(999n, special)).toBe(0n)
})
