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

const legacyCategories = [
  { id: 'technical_service', name: '技术服务费', label: 'Technical services' },
  {
    id: 'software_development',
    name: '软件开发费',
    label: 'Software development',
  },
] as const

export const invoiceCategoryIdSchema = z
  .string()
  .regex(/^[a-z][a-z0-9_]{0,31}$/, 'Invalid invoice category ID.')

const categoryNameError = 'Use 1–60 characters without control characters.'
export const invoiceCategorySchema = z.object({
  id: invoiceCategoryIdSchema,
  name: z
    .string()
    .refine((name) => !/\p{Cc}/u.test(name), categoryNameError)
    .trim()
    .min(1, categoryNameError)
    .refine((name) => [...name].length <= 60, categoryNameError),
  enabled: z.boolean(),
})
export type InvoiceCategory = z.infer<typeof invoiceCategorySchema>
export const defaultInvoiceCategories: InvoiceCategory[] = legacyCategories.map(
  ({ id, name }) => ({ id, name, enabled: true })
)

export function invoiceCategoryNameKey(name: string): string {
  // Default (non-Turkic) folding expands sharp S, unifies sigma variants,
  // preserves dotless i, and uses Cherokee uppercase for case stability.
  return Array.from(name.trim().normalize('NFKC'), (c) => {
    if (c === 'ı') return c
    if (/\p{Script=Cherokee}/u.test(c)) return c.toUpperCase()
    return c.toLowerCase().toUpperCase().toLowerCase()
  })
    .join('')
    .normalize('NFKC')
}

export const invoiceCategoriesSchema = z
  .array(invoiceCategorySchema)
  .max(30, 'Up to 30 invoice categories are allowed.')
  .superRefine((items, ctx) => {
    const ids = new Set<string>()
    const names = new Set<string>()
    for (const [index, item] of items.entries()) {
      const name = invoiceCategoryNameKey(item.name)
      if (ids.has(item.id)) {
        ctx.addIssue({
          code: 'custom',
          path: [index, 'id'],
          message: 'Invoice category IDs must be unique.',
        })
      }
      if (names.has(name)) {
        ctx.addIssue({
          code: 'custom',
          path: [index, 'name'],
          message: 'Invoice category names must be unique.',
        })
      }
      ids.add(item.id)
      names.add(name)
    }
  })

type TranslateCategory = (
  key: (typeof legacyCategories)[number]['label'],
  zh: string
) => string

export function invoiceCategoryOptionLabel(
  category: InvoiceCategory,
  translate: TranslateCategory
): string {
  const legacy = legacyCategories.find((item) => item.id === category.id)
  return legacy?.name === category.name
    ? translate(legacy.label, legacy.name)
    : category.name
}

export function invoiceCategorySnapshotLabel(
  id: string,
  name: string | undefined,
  translate: TranslateCategory
): string {
  if (name) return name
  const legacy = legacyCategories.find((item) => item.id === id)
  return legacy ? translate(legacy.label, legacy.name) : id
}
