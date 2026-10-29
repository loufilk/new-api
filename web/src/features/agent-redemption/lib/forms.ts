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
import type { TFunction } from 'i18next'
import { z } from 'zod'

import { parseQuotaFromDollars } from '@/lib/format'

export function allocationSchema(t: TFunction) {
  return z.object({
    amount: z
      .number()
      .finite()
      .refine(
        (value) =>
          Number.isSafeInteger(parseQuotaFromDollars(value)) &&
          parseQuotaFromDollars(value) !== 0,
        t('Enter a non-zero quota adjustment')
      ),
    reason: z
      .string()
      .trim()
      .min(1, t('Reason is required'))
      .max(200, t('Reason must be 200 characters or fewer')),
  })
}

export function invitationPolicySchema(t: TFunction) {
  return z
    .object({
      enabled: z.boolean(),
      amount: z
        .number()
        .finite()
        .min(0, t('Quota must be a positive number'))
        .refine(
          (value) => Number.isSafeInteger(parseQuotaFromDollars(value)),
          t('Invalid quota amount')
        ),
    })
    .refine(
      (value) => !value.enabled || parseQuotaFromDollars(value.amount) > 0,
      {
        path: ['amount'],
        message: t('Enabled test grants require a positive quota'),
      }
    )
}

export type AllocationForm = z.infer<ReturnType<typeof allocationSchema>>
export type InvitationPolicyForm = z.infer<
  ReturnType<typeof invitationPolicySchema>
>
