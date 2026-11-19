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
import { z } from 'zod'

const text = z.string().trim()
const requiredText = (limit: number) =>
  text
    .min(1, 'Required')
    .refine(
      (value) => [...value].length <= limit && !/\p{Cc}/u.test(value),
      'Invalid field'
    )

export const invoiceSchema = z
  .object({
    invoice_type: z.enum(['ordinary', 'special']),
    title_type: z.literal('company'),
    category: z.enum(['technical_service', 'software_development']),
    title: requiredText(128),
    email: requiredText(254).pipe(z.email('Enter a valid email address.')),
    tax_id: text.regex(
      /^[A-Za-z0-9]{15,20}$/,
      'Enter a 15–20 character tax ID using letters and digits.'
    ),
    address: text.optional(),
    phone: text.optional(),
    bank_name: text.optional(),
    bank_account: text.optional(),
  })
  .superRefine((values, ctx) => {
    if (values.invoice_type !== 'special') return
    for (const [field, limit] of [
      ['address', 255],
      ['bank_name', 128],
    ] as const) {
      const result = requiredText(limit).safeParse(values[field] || '')
      if (!result.success) {
        ctx.addIssue({
          code: 'custom',
          path: [field],
          message: result.error.issues[0].message,
        })
      }
    }
    if (
      !/^[\d+() -]{1,32}$/.test(values.phone || '') ||
      !/\d/.test(values.phone || '')
    ) {
      ctx.addIssue({
        code: 'custom',
        path: ['phone'],
        message: 'Enter a valid phone number.',
      })
    }
    if (!/^\d{1,40}$/.test(values.bank_account || '')) {
      ctx.addIssue({
        code: 'custom',
        path: ['bank_account'],
        message: 'Enter the bank account using digits only.',
      })
    }
  })
  .transform(
    ({ tax_id, address, phone, bank_name, bank_account, ...fields }) => ({
      ...fields,
      tax_id: tax_id.toUpperCase(),
      ...(fields.invoice_type === 'special' && {
        address,
        phone,
        bank_name,
        bank_account,
      }),
    })
  )

export type InvoiceFormInput = z.input<typeof invoiceSchema>
export type InvoiceFields = z.output<typeof invoiceSchema>
