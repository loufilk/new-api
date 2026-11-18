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

export const userGroupPricingSchema = z.object({
  user_id: z.number().int().positive(),
  revision: z.number().int().nonnegative(),
  ratios: z.record(z.string(), z.number().finite().nonnegative()),
  updated_at: z.number().int().nonnegative(),
  updated_by: z.number().int().nonnegative(),
  groups: z.array(
    z.object({
      name: z.string(),
      description: z.string(),
      base_ratio: z.number().finite().nonnegative(),
      effective_ratio: z.number().finite().nonnegative(),
      available: z.boolean(),
    })
  ),
})

export const groupPricingFormSchema = z
  .object({
    rules: z.array(
      z.object({
        group: z
          .string()
          .min(1, 'Select a billing group')
          .refine(
            (group) => group !== 'auto',
            'Select an actual billing group instead of Auto'
          ),
        ratio: z
          .string()
          .trim()
          .min(1, 'Enter a personal multiplier or remove the rule to inherit')
          .refine(
            (value) => Number.isFinite(Number(value)) && Number(value) >= 0,
            'The multiplier must be a finite, non-negative number'
          ),
      })
    ),
  })
  .superRefine((values, context) => {
    const groups = new Set<string>()
    for (const [index, rule] of values.rules.entries()) {
      if (groups.has(rule.group)) {
        context.addIssue({
          code: 'custom',
          path: ['rules', index, 'group'],
          message: 'Each billing group can have only one personal multiplier',
        })
      }
      groups.add(rule.group)
    }
  })

export type GroupPricingFormValues = z.infer<typeof groupPricingFormSchema>

export function groupPricingFormDefaults(
  ratios: Record<string, number>
): GroupPricingFormValues {
  return {
    rules: Object.entries(ratios).map(([group, ratio]) => ({
      group,
      ratio: String(ratio),
    })),
  }
}
