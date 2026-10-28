import type { TFunction } from 'i18next'
import { z } from 'zod'

import { parseQuotaFromDollars } from '@/lib/format'

export function allocationSchema(t: TFunction) {
  return z.object({
    amount: z.number().finite().refine((value) => Number.isSafeInteger(parseQuotaFromDollars(value)) && parseQuotaFromDollars(value) !== 0, t('Enter a non-zero quota adjustment')),
    reason: z.string().trim().min(1, t('Reason is required')).max(200, t('Reason must be 200 characters or fewer')),
  })
}

export function invitationPolicySchema(t: TFunction) {
  return z.object({
    enabled: z.boolean(),
    amount: z.number().finite().min(0, t('Quota must be a positive number')).refine((value) => Number.isSafeInteger(parseQuotaFromDollars(value)), t('Invalid quota amount')),
  }).refine((value) => !value.enabled || parseQuotaFromDollars(value.amount) > 0, { path: ['amount'], message: t('Enabled test grants require a positive quota') })
}

export type AllocationForm = z.infer<ReturnType<typeof allocationSchema>>
export type InvitationPolicyForm = z.infer<ReturnType<typeof invitationPolicySchema>>
