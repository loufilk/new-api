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
import type { ModelDisplayCategory } from '@/lib/model-display-categories'

// ----------------------------------------------------------------------------
// Pricing Types
// ----------------------------------------------------------------------------

export type PricingVendor = {
  id: number
  name: string
  icon?: string
  description?: string
}

export type BillingUsageUnit = 'second' | 'count' | 'token' | 'credit'

export type BillingUsageFieldSchema = {
  type?: 'number' | 'boolean'
  unit?: BillingUsageUnit
  unitLabel?: string | Record<string, string>
  enum?: string[]
  enumLabels?: Record<string, string | Record<string, string>>
  description?: string | Record<string, string>
}

export type BillingUsageSchema = Record<string, BillingUsageFieldSchema>

export type BillingUsageExample = {
  label: string
  facts: Record<string, string | number>
}

export type BillingPluginVariant = {
  plugin_key: string
  plugin_name: string
  icon?: string
  billing_expr: string
  billing_mode?: 'ratio' | 'tiered_expr'
  billing_usage_schema: BillingUsageSchema
  billing_usage_examples?: BillingUsageExample[]
}

export type PricingModel = {
  billing_plugin_variants?: BillingPluginVariant[]
  // Requests outside the plugin variants still pay the model-level price.
  ordinary_pricing?: boolean
  id: number
  model_name: string
  description?: string
  icon?: string
  vendor_id?: number
  vendor_name?: string
  vendor_icon?: string
  vendor_description?: string
  quota_type: number
  model_ratio: number
  completion_ratio: number
  model_price?: number
  cache_ratio?: number | null
  create_cache_ratio?: number | null
  image_ratio?: number | null
  audio_ratio?: number | null
  audio_completion_ratio?: number | null
  enable_groups: string[]
  tags?: string
  supported_endpoint_types?: string[]
  key?: string
  group_keys_are_display_only?: boolean
  group_catalog?: MarketGroupCatalog
  group_market_activity?: MarketActivityByGroup
  group_rate_source?: Record<string, 'personal' | 'group_override' | 'group'>
  group_rate_limits?: Record<string, MarketGroupRateLimit>
  group_ratio?: Record<string, number>
  /** Billing mode (e.g. "tiered_expr") used to flag dynamic pricing */
  billing_mode?: string
  /** Raw expression describing dynamic / tiered billing */
  billing_expr?: string
  /** Task-plugin usage facts and their billing units. */
  billing_usage_schema?: BillingUsageSchema
  /** Display-only labeled usage vectors for pricing examples. */
  billing_usage_examples?: BillingUsageExample[]
  /** Pricing version returned by backend, useful for cache busting */
  pricing_version?: string
  /**
   * Optional model metadata fields reserved for backend-provided catalog data.
   * Keep them data-driven; do not synthesize display values on the client.
   */
  context_length?: number
  max_output_tokens?: number
  knowledge_cutoff?: string
  release_date?: string
  parameter_count?: string
  input_modalities?: Modality[]
  output_modalities?: Modality[]
  capabilities?: string[]
  /** Local catalog filter override; absent/empty retains automatic classification. */
  display_categories?: ModelDisplayCategory[] | null
}

/** Input/output modalities supported by a model. */
export type Modality = 'text' | 'image' | 'audio' | 'video' | 'file'

/** Functional capabilities a model exposes. */
export type ModelCapability =
  | 'function_calling'
  | 'streaming'
  | 'vision'
  | 'json_mode'
  | 'structured_output'
  | 'reasoning'
  | 'tools'
  | 'system_prompt'
  | 'web_search'
  | 'code_interpreter'
  | 'caching'
  | 'embeddings'

export interface MarketGroup {
  label?: string
  description?: string
  platform?: string
  icon?: string
  sort_order?: number
  exclusive?: boolean
  badges?: string[]
}
/** Per-user request buckets, shared across models. Zero means this dimension is not capped. */
export interface MarketGroupRateLimit {
  window_minutes: number
  total_requests: number
  successful_requests: number
  scope: 'shared' | 'group'
}

/** Confirmed changes to the base group multiplier, never personal prices. */
export interface MarketRateChange {
  old_ratio: number
  new_ratio: number
  effective_at: number
}

export interface GroupMarketActivity {
  first_configured_at?: number
  is_new?: true
  rate_changes?: MarketRateChange[]
  recent_rate_change?: true
}

export type MarketActivityByGroup = Record<string, GroupMarketActivity>

export type MarketGroupCatalog = Record<string, MarketGroup>

/** Authorized model names in server order. No raw usage is included. */
export type ModelOrder = Record<'recommended' | 'requests' | 'tokens', string[]>

export type PricingData = {
  model_order?: ModelOrder
  group_keys_are_display_only?: boolean
  group_catalog?: MarketGroupCatalog
  group_market_activity?: MarketActivityByGroup
  group_rate_source?: Record<string, 'personal' | 'group_override' | 'group'>
  group_rate_limits?: Record<string, MarketGroupRateLimit>
  success: boolean
  message?: string
  data: PricingModel[]
  vendors: PricingVendor[]
  group_ratio: Record<string, number>
  usable_group: Record<string, { desc: string; ratio: number }>
  supported_endpoint: Record<string, { path?: string; method?: string }>
  auto_groups: string[]
}

export type TokenUnit = 'M' | 'K'
export type PriceType =
  | 'input'
  | 'output'
  | 'cache'
  | 'create_cache'
  | 'image'
  | 'audio_input'
  | 'audio_output'
export type QuotaType = 0 | 1 // 0: token-based, 1: per-request
