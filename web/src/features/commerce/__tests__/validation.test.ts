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

import { invoiceSchema, partnerSchema } from '../lib/schema'

const invoice = {
  order_ids: [1],
  kind: 'general',
  title_type: 'company',
  title: 'Example Company',
  tax_id: 'DEMO-TAX',
  email: 'invoice@example.com',
  address: '',
  phone: '',
  bank_name: '',
  bank_account: '',
}
const partner = {
  site_name: 'Example API',
  site_url: 'https://supplier.example.com',
  contact_name: 'Supplier',
  contact_email: 'partner@example.com',
  contact_method: '',
  description: 'Native protocol supply with service support.',
  quotes: [
    { group: 'Stable', models: 'example-model', rate: '0.35', description: '' },
  ],
}

describe('invoice application validation', () => {
  test('accepts a verified redemption order without requiring a recharge order', () => {
    expect(
      invoiceSchema.safeParse({
        ...invoice,
        order_ids: [],
        redemption_ids: [71],
        category: 'technical_service',
      }).success
    ).toBe(true)
  })
  test('rejects an unsupported invoice category', () => {
    expect(
      invoiceSchema.safeParse({ ...invoice, category: 'unknown' }).success
    ).toBe(false)
  })
  test('accepts an eligible company application and trims its title', () => {
    expect(
      invoiceSchema.parse({ ...invoice, title: ' Example Company ' }).title
    ).toBe('Example Company')
  })
  test('rejects an application without selected orders', () => {
    expect(invoiceSchema.safeParse({ ...invoice, order_ids: [] }).success).toBe(
      false
    )
  })
  test('rejects repeated orders instead of inflating the preview amount', () => {
    expect(
      invoiceSchema.safeParse({ ...invoice, order_ids: [1, 1] }).success
    ).toBe(false)
  })
  test('requires a tax identifier for company titles', () => {
    expect(invoiceSchema.safeParse({ ...invoice, tax_id: '' }).success).toBe(
      false
    )
    expect(
      invoiceSchema.safeParse({
        ...invoice,
        title_type: 'individual',
        tax_id: '',
      }).success
    ).toBe(true)
  })
  test('requires company registration and bank details for special invoices', () => {
    expect(
      invoiceSchema.safeParse({ ...invoice, kind: 'special' }).success
    ).toBe(false)
    expect(
      invoiceSchema.safeParse({
        ...invoice,
        kind: 'special',
        address: 'Example address',
        phone: '123456',
        bank_name: 'Example bank',
        bank_account: '123456',
      }).success
    ).toBe(true)
  })
  test('rejects an invalid delivery address', () => {
    expect(
      invoiceSchema.safeParse({ ...invoice, email: 'invalid' }).success
    ).toBe(false)
  })
})

describe('partner application validation', () => {
  test('accepts zero as an explicit quoted rate and preserves decimal text', () => {
    expect(
      partnerSchema.parse({
        ...partner,
        quotes: [{ ...partner.quotes[0], rate: '0' }],
      }).quotes[0].rate
    ).toBe('0')
  })
  test.each([
    'http://supplier.example.com',
    'https://user:secret@supplier.example.com',
    'javascript:alert(1)',
    'not a URL',
  ])('rejects an unsafe website %s without throwing', (site_url) => {
    expect(partnerSchema.safeParse({ ...partner, site_url }).success).toBe(
      false
    )
  })
  test('rejects duplicate quoted group names', () => {
    expect(
      partnerSchema.safeParse({
        ...partner,
        quotes: [partner.quotes[0], partner.quotes[0]],
      }).success
    ).toBe(false)
  })
  test.each(['-0.1', 'NaN', '', '0.1234567', '1001', '1e1000000000'])(
    'rejects invalid quoted rate %s',
    (rate) => {
      expect(
        partnerSchema.safeParse({
          ...partner,
          quotes: [{ ...partner.quotes[0], rate }],
        }).success
      ).toBe(false)
    }
  )
  test('requires at least one quote', () => {
    expect(partnerSchema.safeParse({ ...partner, quotes: [] }).success).toBe(
      false
    )
  })
})
