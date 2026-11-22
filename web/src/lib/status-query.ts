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
import { queryOptions, type QueryClient } from '@tanstack/react-query'

import { getStatus } from '@/lib/api'
import { DEFAULT_LOGO, DEFAULT_SYSTEM_NAME } from '@/lib/constants'
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
  type CurrencyConfig,
  type CurrencyDisplayType,
  type SystemConfig,
} from '@/stores/system-config-store'

export const STATUS_QUERY_KEY = ['status'] as const
export const STATUS_STALE_TIME = 5 * 60 * 1000
export const STATUS_GC_TIME = 30 * 60 * 1000
export const STATUS_CACHE_KEY = 'status'
export const STATUS_CACHE_UPDATED_AT_KEY = 'status:updatedAt'

export type StatusRecord = Record<string, unknown>

interface StatusApiData {
  system_name?: string
  logo?: string
  footer_html?: string
  demo_site_enabled?: boolean
  display_token_stat_enabled?: boolean
  display_in_currency?: boolean
  quota_display_type?: CurrencyDisplayType
  quota_per_unit?: number
  usd_exchange_rate?: number
  custom_currency_symbol?: string
  custom_currency_exchange_rate?: number
  [key: string]: unknown
}

function toNumber(value: unknown, fallback: number): number {
  if (typeof value === 'number' && !Number.isNaN(value)) return value
  if (typeof value === 'string') {
    const parsed = Number(value)
    if (!Number.isNaN(parsed)) return parsed
  }
  return fallback
}

/**
 * Map `/api/status` response data to the persisted system config structure.
 * Single canonical mapping shared by the status query and the system config
 * hook so brand, footer and currency settings stay consistent.
 */
export function mapStatusDataToConfig(
  data: StatusApiData | undefined
): Partial<SystemConfig> {
  if (!data) return {}

  const quotaDisplayType =
    (data.quota_display_type as CurrencyDisplayType | undefined) ??
    DEFAULT_CURRENCY_CONFIG.quotaDisplayType

  const currency: CurrencyConfig = {
    displayInCurrency:
      data.display_in_currency ?? DEFAULT_CURRENCY_CONFIG.displayInCurrency,
    quotaDisplayType,
    quotaPerUnit: toNumber(
      data.quota_per_unit,
      DEFAULT_CURRENCY_CONFIG.quotaPerUnit
    ),
    usdExchangeRate: toNumber(
      data.usd_exchange_rate,
      DEFAULT_CURRENCY_CONFIG.usdExchangeRate
    ),
    customCurrencySymbol:
      data.custom_currency_symbol?.trim() ||
      DEFAULT_CURRENCY_CONFIG.customCurrencySymbol,
    customCurrencyExchangeRate: toNumber(
      data.custom_currency_exchange_rate,
      DEFAULT_CURRENCY_CONFIG.customCurrencyExchangeRate
    ),
  }

  return {
    systemName: data.system_name || DEFAULT_SYSTEM_NAME,
    logo: data.logo || DEFAULT_LOGO,
    footerHtml: data.footer_html,
    demoSiteEnabled: data.demo_site_enabled,
    displayTokenStatEnabled: data.display_token_stat_enabled,
    currency,
  }
}

export function readCachedStatus(): StatusRecord | null {
  try {
    if (typeof window === 'undefined') return null
    const raw = window.localStorage.getItem(STATUS_CACHE_KEY)
    if (!raw) return null
    const parsed: unknown = JSON.parse(raw)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return null
    }
    return parsed as StatusRecord
  } catch {
    return null
  }
}

export function readCachedStatusUpdatedAt(): number | null {
  try {
    if (typeof window === 'undefined') return null
    const raw = window.localStorage.getItem(STATUS_CACHE_UPDATED_AT_KEY)
    if (!raw) return null
    const parsed = Number(raw)
    if (!Number.isFinite(parsed) || parsed <= 0) return null
    return parsed
  } catch {
    return null
  }
}

export function isCachedStatusFresh(now: number = Date.now()): boolean {
  const updatedAt = readCachedStatusUpdatedAt()
  if (updatedAt === null) return false
  return updatedAt <= now && now - updatedAt < STATUS_STALE_TIME
}

export function writeCachedStatus(status: StatusRecord | null): void {
  try {
    if (typeof window === 'undefined' || !status) return
    window.localStorage.setItem(STATUS_CACHE_KEY, JSON.stringify(status))
    window.localStorage.setItem(STATUS_CACHE_UPDATED_AT_KEY, String(Date.now()))
  } catch {
    /* empty */
  }
}

function getInitialStatus(): StatusRecord | undefined {
  return readCachedStatus() ?? undefined
}

function syncStatusToSystemConfig(status: StatusRecord | null): void {
  try {
    if (!status) return
    const { setConfig } = useSystemConfigStore.getState()
    setConfig(mapStatusDataToConfig(status as StatusApiData))
  } catch (err) {
    if (import.meta.env.DEV) {
      // eslint-disable-next-line no-console
      console.warn('[statusQuery] Failed to sync status to system config', err)
    }
  }
}

async function fetchStatusAndSync(): Promise<StatusRecord | null> {
  const status = (await getStatus()) as StatusRecord | null
  syncStatusToSystemConfig(status)
  writeCachedStatus(status)
  return status
}

/**
 * Single shared `/api/status` query definition.
 * All brand refreshes, hooks, nav helpers and route guards must go through
 * this instead of calling `getStatus()` or `fetch('/api/status')` directly
 * so the SPA issues at most one in-flight status request per stale window.
 */
export const statusQueryOptions = queryOptions({
  queryKey: STATUS_QUERY_KEY,
  queryFn: fetchStatusAndSync,
  placeholderData: getInitialStatus,
  staleTime: STATUS_STALE_TIME,
  gcTime: STATUS_GC_TIME,
})

/**
 * Boot-time prefetch: a fresh localStorage cache is installed into the query
 * cache immediately without a network round trip; otherwise one network
 * fetch refreshes the cache. Failures keep the last cached value.
 */
export async function prefetchStatusAtStartup(
  queryClient: QueryClient
): Promise<StatusRecord | null> {
  const cached = readCachedStatus()
  if (cached && isCachedStatusFresh()) {
    queryClient.setQueryData(STATUS_QUERY_KEY, cached)
    syncStatusToSystemConfig(cached)
    return cached
  }
  try {
    return await queryClient.fetchQuery(statusQueryOptions)
  } catch {
    if (cached) {
      queryClient.setQueryData(STATUS_QUERY_KEY, cached)
      syncStatusToSystemConfig(cached)
      return cached
    }
    return null
  }
}

/**
 * Shared-query read for route guards and other imperative callers.
 * Returns fresh query data when possible, otherwise the last cached value.
 * Callers that must fail closed (e.g. the image module without any cache)
 * treat a `null` return as disabled.
 */
export async function ensureSharedStatus(
  queryClient: QueryClient
): Promise<StatusRecord | null> {
  try {
    const data = await queryClient.ensureQueryData({
      ...statusQueryOptions,
      revalidateIfStale: true,
    })
    return (data as StatusRecord | null) ?? readCachedStatus()
  } catch {
    return (
      queryClient.getQueryData<StatusRecord>(STATUS_QUERY_KEY) ??
      readCachedStatus()
    )
  }
}

// Compatibility name retained for route and hook callers shipped with the
// custom console; both names use the same deduplicated query path.
export const ensureStatus = ensureSharedStatus
