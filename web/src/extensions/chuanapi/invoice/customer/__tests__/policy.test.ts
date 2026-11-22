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
  invoiceCategoryOptionLabel,
  invoiceCategorySnapshotLabel,
} from '../../categories'
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

test('only an absent catalog inherits the two legacy categories; empty, disabled and malformed catalogs never reopen them', () => {
  const base = { version: 6, enabled: true, ordinary, special }
  const custom = { id: 'hosting_2', name: 'Server hosting', enabled: true }
  expect(
    customerConfigSchema.safeParse({ enabled: true, categories: [custom] })
      .success
  ).toBe(false)
  expect(customerConfigSchema.parse(base).categories).toEqual([
    { id: 'technical_service', name: '技术服务费', enabled: true },
    { id: 'software_development', name: '软件开发费', enabled: true },
  ])
  expect(
    customerConfigSchema.parse({ ...base, categories: [] }).categories
  ).toEqual([])
  expect(
    customerConfigSchema.parse({
      ...base,
      categories: [
        custom,
        { ...custom, id: 'old', name: 'Old', enabled: false },
      ],
    }).categories
  ).toEqual([custom])
  for (const categories of [
    null,
    [{}],
    [{ ...custom, id: 'not allowed!' }],
    [custom, { ...custom, id: 'other', name: 'Ｓerver Hosting' }],
  ]) {
    expect(
      customerConfigSchema.safeParse({ ...base, categories }).success
    ).toBe(false)
  }
})

test('only the original built-in choices and records without a name are translated', () => {
  const technical = {
    id: 'technical_service',
    name: '技术服务费',
    enabled: true,
  }
  const translate = (key: string) => key
  expect(invoiceCategoryOptionLabel(technical, translate)).toBe(
    'Technical services'
  )
  expect(
    invoiceCategoryOptionLabel(
      { ...technical, name: 'Edited label' },
      translate
    )
  ).toBe('Edited label')
  expect(
    invoiceCategoryOptionLabel({ ...technical, id: 'hosting' }, translate)
  ).toBe('技术服务费')
  expect(invoiceCategorySnapshotLabel(technical.id, undefined, translate)).toBe(
    'Technical services'
  )
  expect(
    invoiceCategorySnapshotLabel(technical.id, technical.name, translate)
  ).toBe(technical.name)
  expect(invoiceCategorySnapshotLabel('custom_id', undefined, translate)).toBe(
    'custom_id'
  )
})
