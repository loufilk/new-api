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
import i18next from 'i18next'
import { z } from 'zod'

import type { OperationDraft, PreparedOperation } from '../types'

const integer = z.number().int().nonnegative().max(Number.MAX_SAFE_INTEGER)
const minor = z.string().regex(/^(0|[1-9]\d*)$/)
const currency = z.string().regex(/^([A-Z]{3})?$/)
const preparedSchema = z.object({
  verification_context: z.object({
    action: z.string(),
    request_id: z.string(),
    agent_user_id: integer,
    profile_version: integer,
    policy_version: integer,
    currency,
    amount_minor: minor,
    quota: integer,
    target_type: z.string(),
    target_id: z.string(),
    target_version: integer,
    recipient_version: integer,
    recipient_digest: z.string(),
    parameters_hash: z.string().min(1),
  }),
  summary: z.object({
    action: z.string(),
    agent_user_id: integer,
    currency,
    currency_exponent: integer.max(6),
    amount_minor: minor,
    fee_minor: minor,
    net_minor: minor,
    quota: integer,
    target_label: z.string(),
    recipient_label: z.string(),
    policy_version: integer,
  }),
  status_url: z.string(),
})
export function validatePreparedOperation(
  value: unknown,
  draft: Pick<OperationDraft, 'action' | 'request_id'>
): PreparedOperation {
  const parsed = preparedSchema.safeParse(value)
  if (!parsed.success) {
    throw new Error(
      i18next.t('The transaction review is incomplete. Nothing was authorized.')
    )
  }
  const { verification_context: context, summary } = parsed.data
  if (
    context.request_id !== draft.request_id ||
    context.action !== draft.action ||
    summary.action !== context.action ||
    summary.agent_user_id !== context.agent_user_id ||
    summary.amount_minor !== context.amount_minor ||
    summary.currency !== context.currency ||
    summary.quota !== context.quota ||
    summary.policy_version !== context.policy_version
  ) {
    throw new Error(
      i18next.t(
        'The transaction review does not match the requested operation. Nothing was authorized.'
      )
    )
  }
  return parsed.data
}
