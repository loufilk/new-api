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
import { catalogIconID } from '@/lib/catalog-icon-registry'

import { FILTER_ALL } from '../constants'
import type { MarketGroupCatalog, PricingModel } from '../types'
import {
  groupPlatform,
  MARKET_PLATFORMS,
  MARKET_PLATFORM_ICONS,
} from './market-groups'

type Brand = { key: string; label: string; icon: string | null }
const other: Brand = { key: 'other', label: 'Other', icon: null }

// Exact vendor labels from the public vendor directory. A model ID, protocol,
// price, group ID or logo alone does not prove the model's vendor.
const brands = [
  ['openai', 'OpenAI', 'OpenAI', ['openai']],
  ['anthropic', 'Anthropic', 'Claude.Color', ['anthropic', 'claude']],
  ['gemini', 'Gemini', 'Gemini.Color', ['gemini', 'google', 'google deepmind']],
  ['deepseek', 'DeepSeek', 'DeepSeek.Color', ['deepseek', '深度求索']],
  [
    'moonshot',
    'Moonshot',
    'Moonshot',
    ['moonshot', 'moonshot ai', 'moonshotai', 'kimi', '月之暗面'],
  ],
  ['xai', 'xAI', 'XAI', ['xai', 'x.ai']],
  [
    'zhipu',
    'GLM',
    'Zhipu.Color',
    ['zhipu', 'zhipu ai', 'zhipuai', '智谱', '智谱ai', 'glm', 'z.ai'],
  ],
  ['azure', 'Azure', 'Azure.Color', ['azure', 'microsoft azure']],
  ['alibaba', 'Alibaba', 'Qwen.Color', ['阿里巴巴', 'alibaba', 'qwen']],
  ['minimax', 'MiniMax', 'Minimax.Color', ['minimax', 'minimax (minimax.io)']],
  ['xiaomi', 'Xiaomi', 'XiaomiMiMo', ['xiaomi', '小米']],
  ['meta', 'Meta', 'Meta.Color', ['meta']],
] as const

const byLabel = new Map<string, Brand>()
for (const [key, label, icon, aliases] of brands) {
  for (const alias of aliases) byLabel.set(alias, { key, label, icon })
}

export function publicModelBrand(
  model: Pick<PricingModel, 'vendor_id' | 'vendor_name' | 'vendor_icon'>
): Brand {
  const label = model.vendor_name?.trim()
  if (!label) return other
  const normalized = label.toLowerCase()
  const known = byLabel.get(normalized)
  if (known) return known
  if (
    !model.vendor_id ||
    ['other', 'unknown', '其他', '未知'].includes(normalized)
  )
    return other
  return {
    key: `vendor-${model.vendor_id}`,
    label,
    icon: catalogIconID(model.vendor_icon) || null,
  }
}

// Only public data returned for this request participates. Never substitute
// operator names or present a tier shared by different vendors as a real group.
export function publicTierCatalog(
  models: PricingModel[],
  ratios: Record<string, number>
): MarketGroupCatalog {
  const consensus = new Map<string, Brand | null>()
  for (const model of models) {
    const brand = publicModelBrand(model)
    for (const tier of model.enable_groups ?? []) {
      const previous = consensus.get(tier)
      consensus.set(
        tier,
        previous === undefined || previous?.key === brand.key ? brand : null
      )
    }
  }
  return Object.fromEntries(
    Object.entries(ratios)
      .filter(([, rate]) => Number.isFinite(rate) && rate >= 0)
      .map(([tier, rate]) => [
        tier,
        {
          label: `×${rate}`,
          sort_order: rate,
          public_tier_icon: consensus.get(tier)?.icon ?? undefined,
        },
      ])
  )
}

// Guests filter models by brand, then drill down into anonymized rate tiers.
// For members, the same control chooses real, authorized routing groups instead.
export function marketFacets(
  models: PricingModel[],
  groups: readonly string[],
  catalog: MarketGroupCatalog | undefined,
  requested: string | undefined,
  displayTiers: boolean
) {
  const options = new Map<string, Brand & { count: number }>()
  if (displayTiers) {
    for (const model of models) {
      const brand = publicModelBrand(model)
      const existing = options.get(brand.key)
      if (existing) existing.count++
      else options.set(brand.key, { ...brand, count: 1 })
    }
  } else {
    for (const group of groups) {
      const key = groupPlatform(catalog?.[group])
      options.set(key, {
        key,
        label: MARKET_PLATFORMS[key],
        icon: MARKET_PLATFORM_ICONS[key],
        count: 1 + (options.get(key)?.count ?? 0),
      })
    }
  }
  const platform = requested && options.has(requested) ? requested : FILTER_ALL
  const guestModels =
    displayTiers && platform !== FILTER_ALL
      ? models.filter((m) => publicModelBrand(m).key === platform)
      : models
  const usedTiers = displayTiers
    ? new Set(guestModels.flatMap((m) => m.enable_groups ?? []))
    : undefined
  const visibleGroups = groups.filter((group) =>
    usedTiers
      ? usedTiers.has(group)
      : platform === FILTER_ALL || groupPlatform(catalog?.[group]) === platform
  )
  const priceGroups =
    !displayTiers && platform !== FILTER_ALL
      ? new Set(visibleGroups)
      : undefined
  const visibleModels = priceGroups
    ? models
        .filter((m) => m.enable_groups?.some((g) => priceGroups.has(g)))
        .map((m) => {
          if (!m.supported_endpoint_types_by_group) return m
          // Scope the cross-group documentary union to precisely the groups behind
          // the selected platform. Do not rewrite permissions or Key prefill inputs.
          return {
            ...m,
            supported_endpoint_types_by_group: Object.fromEntries(
              Object.entries(m.supported_endpoint_types_by_group).filter(
                ([g]) => priceGroups.has(g)
              )
            ),
          }
        })
    : guestModels
  return {
    options: [...options.values()].sort(
      (a, b) =>
        Number(a.key === 'other') - Number(b.key === 'other') ||
        a.label.localeCompare(b.label)
    ),
    platform,
    priceGroups,
    visibleModels,
    visibleGroups,
  }
}
