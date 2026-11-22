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
import type { MarketGroup, MarketGroupCatalog, PricingModel } from '../types'

export const MARKET_PLATFORMS = {
  anthropic: 'Anthropic',
  openai: 'OpenAI',
  gemini: 'Gemini',
  zhipu: 'GLM',
  deepseek: 'DeepSeek',
  moonshot: 'Moonshot',
  xai: 'xAI',
  azure: 'Azure',
  other: 'Other',
} as const

export type MarketPlatform = keyof typeof MARKET_PLATFORMS

export function groupPlatform(meta?: MarketGroup): MarketPlatform {
  const key = meta?.platform
  return !meta?.exclusive && key && Object.hasOwn(MARKET_PLATFORMS, key)
    ? (key as MarketPlatform)
    : 'other'
}

export function marketGroupLabel(
  group: string,
  meta?: MarketGroupCatalog
): string {
  return meta?.[group]?.label?.trim() || group
}

export function activeBillingExpressions(models: PricingModel[]): string[] {
  const expr = new Set<string>()
  for (const model of models) {
    if (model.billing_expr) expr.add(model.billing_expr)
    if (!model.ordinary_pricing) {
      for (const variant of model.billing_plugin_variants ?? []) {
        if (variant.billing_expr) expr.add(variant.billing_expr)
      }
    }
  }
  return [...expr]
}
