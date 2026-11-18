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
    imageModels.add(item.model_name)
  }
  if (imageModels.size === 0) return []

  // Routing and token permissions use exact, case-sensitive model IDs.
  const tokenSet = new Set(params.tokenModels)
  return [...imageModels]
    .filter((model) => tokenSet.has(model))
    .sort((a, b) => a.localeCompare(b))
}

export function getEnabledApiKeys<T extends { status: number }>(
  keys: T[]
): T[] {
  return keys.filter((key) => key.status === 1)
}
