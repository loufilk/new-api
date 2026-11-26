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
import { requireServerSuccess } from '@/lib/server-error-message'

export const USDT_NETWORKS = [
  { id: 'bsc', label: 'USDT-BSC' },
  { id: 'polygon', label: 'USDT-POLYGON' },
  { id: 'tron', label: 'USDT-TRC20' },
] as const

export const USDT_ADMIN_QUERY_KEY = ['system-options', 'usdt'] as const

const centsTextPattern = /^\d{1,7}(?:\.\d{1,2})?$/
const amountSchema = z
  .string()
  .refine(
    (value) =>
      centsTextPattern.test(value) &&
      Number(value) >= 0.02 &&
      Number(value) <= 1_000_000,
    'Enter 0.02 to 1,000,000 CNY with up to two decimal places.'
  )
const bonusSchema = z
  .string()
  .refine(
    (value) => centsTextPattern.test(value) && Number(value) <= 100,
    'Enter 0–100 with up to two decimal places.'
  )
const netForm = z.object({ enabled: z.boolean(), bonus: bonusSchema })
const policyFields = {
  enabled: z.boolean(),
  min_amount: amountSchema,
  max_amount: amountSchema,
}

export const usdtSettingsFormSchema = z
  .object({
    ...policyFields,
    networks: z.object({ bsc: netForm, polygon: netForm, tron: netForm }),
  })
  .check((ctx) => {
    const values = ctx.value
    if (Number(values.max_amount) < Number(values.min_amount)) {
      ctx.issues.push({
        code: 'custom',
        input: values.max_amount,
        path: ['max_amount'],
        message: 'Maximum cannot be lower than minimum.',
      })
    }
    if (
      values.enabled &&
      !Object.values(values.networks).some((network) => network.enabled)
    ) {
      ctx.issues.push({
        code: 'custom',
        input: values.enabled,
        path: ['enabled'],
        message: 'Enable at least one USDT network.',
      })
    }
  })

const responseSchema = z.object({
  ...policyFields,
  configured: z.boolean(),
  networks: z
    .array(
      z.object({
        id: z.enum(['bsc', 'polygon', 'tron']),
        enabled: z.boolean(),
        bonus_bps: z.number().int().min(0).max(10000),
      })
    )
    .length(3)
    .refine(
      (networks) => new Set(networks.map((network) => network.id)).size === 3
    ),
})

export type UsdtSettingsForm = z.infer<typeof usdtSettingsFormSchema>
export type UsdtSettings = z.infer<typeof responseSchema>

export function toUsdtSettingsForm(settings: UsdtSettings): UsdtSettingsForm {
  const networks = {} as UsdtSettingsForm['networks']
  for (const network of settings.networks) {
    networks[network.id] = {
      enabled: network.enabled,
      bonus: (network.bonus_bps / 100).toFixed(2),
    }
  }
  return {
    enabled: settings.enabled,
    min_amount: settings.min_amount,
    max_amount: settings.max_amount,
    networks,
  }
}

function parseSettings(body: { data?: unknown }): UsdtSettings {
  const parsed = responseSchema.safeParse(requireServerSuccess(body)?.data)
  if (!parsed.success) throw new Error('Invalid USDT settings response.')
  return parsed.data
}

export async function loadUsdtSettings(
  signal: AbortSignal
): Promise<UsdtSettings> {
  const response = await api.get('/api/option/usdt', { signal })
  return parseSettings(response.data)
}

export async function saveUsdtSettings(
  values: UsdtSettingsForm
): Promise<UsdtSettings> {
  const response = await api.put('/api/option/usdt', {
    enabled: values.enabled,
    min_amount: values.min_amount,
    max_amount: values.max_amount,
    networks: USDT_NETWORKS.map(({ id }) => {
      const [whole, fraction = ''] = values.networks[id].bonus.split('.')
      return {
        id,
        enabled: values.networks[id].enabled,
        bonus_bps: Number(whole) * 100 + Number(fraction.padEnd(2, '0')),
      }
    }),
  })
  return parseSettings(response.data)
}
