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

import { api } from '@/lib/api'
import {
  createServerError,
  requireServerSuccess,
} from '@/lib/server-error-message'

export const supportEmailSettingsSchema = z.object({
  enabled: z.boolean(),
  admin_recipients: z.array(z.string()).max(5),
  site_origin: z.string(),
  smtp_configured: z.boolean(),
})
export type SupportEmailSettings = z.infer<typeof supportEmailSettingsSchema>
export type SupportEmailSettingsInput = Omit<
  SupportEmailSettings,
  'smtp_configured'
>

export const supportEmailPreferencesSchema = z.object({
  enabled: z.boolean(),
  email: z.string(),
  email_verified: z.boolean(),
  system_enabled: z.boolean(),
})
export type SupportEmailPreferences = z.infer<
  typeof supportEmailPreferencesSchema
>

const outboxItemSchema = z.object({
  id: z.number().int().positive(),
  ticket_id: z.number().int().positive(),
  event: z.string().max(32),
  status: z.string().max(32),
  attempts: z.number().int().nonnegative(),
  next_attempt_at: z.number().int().nonnegative(),
  last_error_code: z.string().max(100),
  created_at: z.number().int().nonnegative(),
})
const outboxSchema = z.object({
  items: z.array(outboxItemSchema),
  total: z.number().int().nonnegative(),
})
export type SupportEmailOutboxItem = z.infer<typeof outboxItemSchema>
const envelopeSchema = z.object({
  success: z.literal(true),
  data: z.unknown().optional(),
})
const mutationConfig = { skipBusinessError: true, skipErrorHandler: true }

function responseData(response: unknown): unknown {
  requireServerSuccess(response)
  const envelope = envelopeSchema.safeParse(response)
  if (!envelope.success) {
    throw createServerError({ message: 'Invalid server response' })
  }
  return envelope.data.data
}

export async function getSupportEmailSettings(
  signal?: AbortSignal
): Promise<SupportEmailSettings> {
  const response = await api.get('/api/support-email/settings', { signal })
  return supportEmailSettingsSchema.parse(responseData(response.data))
}

export async function saveSupportEmailSettings(
  input: SupportEmailSettingsInput
): Promise<void> {
  const response = await api.put(
    '/api/support-email/settings',
    input,
    mutationConfig
  )
  responseData(response.data)
}

export async function getSupportEmailPreferences(
  signal?: AbortSignal
): Promise<SupportEmailPreferences> {
  const response = await api.get('/api/support-email/preferences', { signal })
  return supportEmailPreferencesSchema.parse(responseData(response.data))
}

export async function saveSupportEmailPreferences(
  enabled: boolean
): Promise<void> {
  const response = await api.put(
    '/api/support-email/preferences',
    { enabled },
    mutationConfig
  )
  responseData(response.data)
}

export async function getSupportEmailOutbox(
  page: number,
  signal?: AbortSignal
): Promise<z.infer<typeof outboxSchema>> {
  const response = await api.get('/api/support-email/outbox', {
    signal,
    params: { p: page, page_size: 20 },
  })
  return outboxSchema.parse(responseData(response.data))
}

export async function updateSupportEmailOutbox(input: {
  id: number
  action: 'retry' | 'cancel'
}): Promise<void> {
  const response = await api.post(
    `/api/support-email/outbox/${input.id}/${input.action}`,
    undefined,
    mutationConfig
  )
  responseData(response.data)
}
