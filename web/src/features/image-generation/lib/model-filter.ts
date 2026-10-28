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
import type { ImageApiKeyOption } from '../types'

type PricingLike = {
  model_name: string
  supported_endpoint_types?: string[]
}

function normalizeModelName(value: string): string {
  return value.trim().toLowerCase()
}

export function getAvailableImageModels(params: {
  pricingModels: PricingLike[]
  tokenModels: string[]
  token: ImageApiKeyOption | null
}): string[] {
  if (!params.token) return []

  const imageModels = new Set<string>()
  for (const item of params.pricingModels) {
    const types = item.supported_endpoint_types ?? []
    if (!types.includes('image-generation')) continue
    if (!item.model_name || item.model_name.trim() === '') continue
    imageModels.add(normalizeModelName(item.model_name))
  }
  if (imageModels.size === 0) return []

  const tokenSet = new Set(
    params.tokenModels.map((model) => normalizeModelName(model))
  )

  // The owned-token endpoint applies routing and model restrictions server-side.
  const allowed = [...imageModels].filter((model) => tokenSet.has(model))
  if (allowed.length === 0) return []

  const displayName = new Map<string, string>()
  for (const item of params.pricingModels) {
    const key = normalizeModelName(item.model_name)
    if (imageModels.has(key) && !displayName.has(key)) {
      displayName.set(key, item.model_name)
    }
  }
  for (const model of params.tokenModels) {
    const key = normalizeModelName(model)
    if (!displayName.has(key)) displayName.set(key, model)
  }

  return allowed
    .map((key) => displayName.get(key) ?? key)
    .sort((a, b) => a.localeCompare(b))
}

export function getEnabledApiKeys<T extends { status: number }>(
  keys: T[]
): T[] {
  return keys.filter((key) => key.status === 1)
}
