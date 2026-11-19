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
import { createServerError } from '@/lib/server-error-message'

import { invoiceSettingsSchema, type InvoiceSettings } from '../contracts'

const settings = invoiceSettingsSchema.extend({
  fee_payment_available: z.boolean().optional(),
})
export type AdminInvoiceSettings = z.infer<typeof settings>

const path = '/api/admin/invoice/settings'
type Envelope = { success: boolean; data?: unknown }

function readSettings(envelope: Envelope): AdminInvoiceSettings {
  if (envelope?.success !== true) throw createServerError(envelope)
  const parsed = settings.safeParse(envelope.data)
  if (!parsed.success) throw createServerError(null)
  return parsed.data
}

export async function getSettings(
  signal: AbortSignal
): Promise<AdminInvoiceSettings> {
  const { data } = await api.get<Envelope>(path, {
    signal,
    disableDuplicate: true,
  })
  return readSettings(data)
}

export async function putSettings(
  body: InvoiceSettings
): Promise<AdminInvoiceSettings> {
  const { data } = await api.put<Envelope>(path, body, {
    singleUseAuthorization: true,
  })
  return readSettings(data)
}
