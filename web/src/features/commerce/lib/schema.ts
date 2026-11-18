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

const httpsURL = z
  .string()
  .trim()
  .url('Enter a valid HTTPS URL')
  .max(2048, 'Input is too long')
  .refine((value) => {
    try {
      const url = new URL(value)
      return url.protocol === 'https:' && !url.username && !url.password
    } catch {
      return false
    }
  }, 'Enter a valid HTTPS URL')

export const invoiceSchema = z
  .object({
    order_ids: z
      .array(z.number().int().positive())
      .min(1, 'Select at least one eligible order')
      .max(100),
    kind: z.enum(['general', 'special']),
    title_type: z.enum(['individual', 'company']),
    title: z
      .string()
      .trim()
      .min(2, 'Enter the invoice title')
      .max(200, 'Input is too long'),
    tax_id: z.string().trim().max(64, 'Input is too long'),
    email: z
      .string()
      .trim()
      .email('Enter a valid email address')
      .max(254, 'Input is too long'),
    address: z.string().trim().max(300, 'Input is too long'),
    phone: z.string().trim().max(64, 'Input is too long'),
    bank_name: z.string().trim().max(200, 'Input is too long'),
    bank_account: z.string().trim().max(80, 'Input is too long'),
  })
  .superRefine((input, context) => {
    if (new Set(input.order_ids).size !== input.order_ids.length) {
      context.addIssue({
        code: 'custom',
        path: ['order_ids'],
        message: 'Select each order only once',
      })
    }
    if (input.title_type === 'company' && !input.tax_id) {
      context.addIssue({
        code: 'custom',
        path: ['tax_id'],
        message: 'Enter the tax identification number',
      })
    }
    if (input.kind === 'special') {
      if (input.title_type !== 'company') {
        context.addIssue({
          code: 'custom',
          path: ['title_type'],
          message: 'Special invoices require a company title',
        })
      }
      for (const field of [
        'address',
        'phone',
        'bank_name',
        'bank_account',
      ] as const) {
        if (!input[field]) {
          context.addIssue({
            code: 'custom',
            path: [field],
            message: 'This field is required for special invoices',
          })
        }
      }
    }
  })

export const partnerSchema = z
  .object({
    site_name: z
      .string()
      .trim()
      .min(2, 'Enter your site name')
      .max(120, 'Input is too long'),
    site_url: httpsURL,
    contact_name: z
      .string()
      .trim()
      .min(1, 'Enter a contact name')
      .max(120, 'Input is too long'),
    contact_email: z
      .string()
      .trim()
      .email('Enter a valid email address')
      .max(254, 'Input is too long'),
    contact_method: z.string().trim().max(200, 'Input is too long'),
    description: z
      .string()
      .trim()
      .min(10, 'Describe your supply and service')
      .max(3000, 'Input is too long'),
    quotes: z
      .array(
        z.object({
          group: z
            .string()
            .trim()
            .min(1, 'Enter a group name')
            .max(120, 'Input is too long'),
          models: z
            .string()
            .trim()
            .min(1, 'Enter supported models')
            .max(500, 'Input is too long'),
          rate: z
            .string()
            .trim()
            .regex(
              /^\d{1,4}(?:\.\d{1,6})?$/,
              'Enter a non-negative rate with up to six decimals'
            )
            .refine(
              (value) => Number(value) <= 1000,
              'Rate must not exceed 1000'
            ),
          description: z.string().trim().max(500, 'Input is too long'),
        })
      )
      .min(1)
      .max(20),
  })
  .superRefine((input, context) => {
    const names = new Set<string>()
    input.quotes.forEach((quote, index) => {
      if (names.has(quote.group)) {
        context.addIssue({
          code: 'custom',
          path: ['quotes', index, 'group'],
          message: 'Group names must be unique',
        })
      }
      names.add(quote.group)
    })
  })

export const commerceSettingsSchema = z.object({
  revision: z.number().int().nonnegative(),
  invoice_enabled: z.boolean(),
  invoice_window_days: z.number().int().min(1).max(3650),
  invoice_max_orders: z.number().int().min(1).max(100),
  invoice_currency: z
    .string()
    .trim()
    .regex(/^[A-Z]{3}$/, 'Use a three-letter currency code'),
  general_fee_bps: z.number().int().min(0).max(10000),
  special_invoice_enabled: z.boolean(),
  special_fee_bps: z.number().int().min(0).max(10000),
  invoice_instructions: z.string().max(3000, 'Input is too long'),
  partner_enabled: z.boolean(),
  partner_instructions: z.string().max(3000, 'Input is too long'),
  updated_at: z.number(),
  updated_by: z.number(),
})
