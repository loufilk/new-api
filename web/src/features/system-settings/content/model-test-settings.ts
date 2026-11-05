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

export const modelTestTargetSchema = z.object({
  id: z.string().uuid(),
  group: z.string().min(1),
  model: z.string().min(1),
  channel_id: z.number().int().positive(),
  enabled: z.boolean(),
  period_secs: z.number().int().nonnegative().max(Number.MAX_SAFE_INTEGER),
  max_output_tokens: z.union([z.literal(2048), z.literal(4096)]),
  max_run_quota: z.number().int().nonnegative().max(Number.MAX_SAFE_INTEGER),
})
export const modelTestSettingsSchema = z.object({
  enabled: z.boolean(),
  version: z.number().int().nonnegative().max(Number.MAX_SAFE_INTEGER),
  automatic_enabled: z.boolean(),
  default_period_secs: z.number().int().min(60).max(604800),
  daily_budget_quota: z
    .number()
    .int()
    .nonnegative()
    .max(Number.MAX_SAFE_INTEGER),
  targets: z.array(modelTestTargetSchema).max(100),
})
export type ModelTestSettingsInput = z.infer<typeof modelTestSettingsSchema>
export type ModelTestTarget = z.infer<typeof modelTestTargetSchema>
export type ModelTestCatalogue = {
  groups: string[]
  models_by_group: Record<string, string[]>
  routes: { id: number; name: string; group: string; model: string }[]
}
export type ModelTestSettings = ModelTestSettingsInput & {
  catalogue: ModelTestCatalogue
  engine_ready: boolean
}

export function canEnableAutomaticTests(
  settings: ModelTestSettingsInput
): boolean {
  const enabled = settings.targets.filter((target) => target.enabled)
  return (
    settings.enabled &&
    settings.daily_budget_quota > 0 &&
    enabled.length > 0 &&
    enabled.every(
      (target) =>
        target.max_run_quota > 0 &&
        target.max_run_quota <= settings.daily_budget_quota
    )
  )
}

export function validateModelTestTargets(
  settings: ModelTestSettingsInput,
  catalogue: ModelTestCatalogue
): boolean {
  if (!modelTestSettingsSchema.safeParse(settings).success) return false
  if (settings.automatic_enabled && !canEnableAutomaticTests(settings)) {
    return false
  }
  const routes = new Set(
    settings.targets.map((target) =>
      JSON.stringify([target.group, target.model])
    )
  )
  if (routes.size !== settings.targets.length) return false
  return settings.targets.every(
    (target) =>
      (target.period_secs === 0 ||
        (target.period_secs >= 60 && target.period_secs <= 604800)) &&
      (!target.enabled || target.max_run_quota > 0) &&
      catalogue.groups.includes(target.group) &&
      catalogue.models_by_group[target.group]?.includes(target.model) &&
      catalogue.routes.some(
        (route) =>
          route.id === target.channel_id &&
          route.group === target.group &&
          route.model === target.model
      )
  )
}

export async function getModelTestSettings(
  signal?: AbortSignal
): Promise<ModelTestSettings> {
  const response = await api.get('/api/model-tests/settings', { signal })
  const result = requireServerSuccess(response.data)
  const parsed = modelTestSettingsSchema.parse(result.data)
  const catalogue = z
    .object({
      groups: z.array(z.string()),
      models_by_group: z.record(z.string(), z.array(z.string())),
      routes: z.array(
        z.object({
          id: z.number().int().positive(),
          name: z.string(),
          group: z.string(),
          model: z.string(),
        })
      ),
    })
    .parse(result.data.catalogue)
  return {
    ...parsed,
    catalogue,
    engine_ready: result.data.engine_ready === true,
  }
}

export async function saveModelTestSettings(
  settings: ModelTestSettingsInput
): Promise<void> {
  const response = await api.put('/api/model-tests/settings', {
    ...modelTestSettingsSchema.parse(settings),
    expected_version: settings.version,
  })
  requireServerSuccess(response.data)
}
