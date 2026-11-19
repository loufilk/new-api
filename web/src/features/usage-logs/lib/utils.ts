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
/**
 * Utility functions for usage logs feature
 */
import type { QueryClient } from '@tanstack/react-query'

import { createServerError } from '@/lib/server-error-message'

import {
  getAllLogs,
  getUserLogs,
  getAllMidjourneyLogs,
  getUserMidjourneyLogs,
  getAllTaskLogs,
  getUserTaskLogs,
} from '../api'
import {
  LOG_TYPES,
  DISPLAYABLE_LOG_TYPES,
  TIMING_LOG_TYPES,
} from '../constants'
import type {
  GetLogsParams,
  GetLogsResponse,
  FetchLogsConfig,
  GetMidjourneyLogsParams,
  GetTaskLogsParams,
} from '../types'
import { LOG_USER_ID_ERROR, logUserIdSearchSchema } from './filter'

export { buildQueryParams } from './query-params'

// ============================================================================
// Type Checkers & Utilities
// ============================================================================

/**
 * Check if log type is displayable (has detailed info)
 */
export function isDisplayableLogType(type: number): boolean {
  return (DISPLAYABLE_LOG_TYPES as readonly number[]).includes(type)
}

/**
 * Check if log type shows timing info
 */
export function isTimingLogType(type: number): boolean {
  return (TIMING_LOG_TYPES as readonly number[]).includes(type)
}

/**
 * Get log type configuration by type number
 */
export function getLogTypeConfig(type: number) {
  return LOG_TYPES.find((t) => t.value === type) || LOG_TYPES[0]
}

/**
 * Check if log uses per-call billing
 */
export function isPerCallBilling(modelPrice?: number): boolean {
  return (modelPrice ?? 0) > 0
}

/**
 * Get default time range (today 00:00:00 to now + 1 hour)
 */
export function getDefaultTimeRange(): { start: Date; end: Date } {
  const now = new Date()
  const start = new Date(now)
  start.setHours(0, 0, 0, 0)
  const end = new Date(now.getTime() + 3600 * 1000) // +1 hour

  return { start, end }
}

/**
 * Convert milliseconds timestamp to seconds for API
 */
function timestampToSeconds(ms: number): number {
  return Math.floor(ms / 1000)
}

/**
 * Build time range parameters with default values
 * Shared logic for all log types
 */
function buildTimeRangeParams(
  searchParams: Record<string, unknown>,
  useMilliseconds: boolean
): { start_timestamp?: number; end_timestamp?: number } {
  const hasTimeParams = searchParams.startTime ?? searchParams.endTime
  const defaultTimeRange = !hasTimeParams ? getDefaultTimeRange() : null

  const convertTimestamp = (timestamp: number) =>
    useMilliseconds ? timestamp : timestampToSeconds(timestamp)

  const getTimestamp = (paramTime?: unknown, defaultTime?: Date) => {
    const time = (paramTime as number) || defaultTime?.getTime()
    return time ? convertTimestamp(time) : undefined
  }

  return {
    start_timestamp: getTimestamp(
      searchParams.startTime,
      defaultTimeRange?.start
    ),
    end_timestamp: getTimestamp(searchParams.endTime, defaultTimeRange?.end),
  }
}

/**
 * Build base parameters with time range (for drawing and task logs)
 * @param useMilliseconds - Whether to use millisecond timestamps (true for drawing logs, false for task logs)
 */
export function buildBaseParams(config: {
  page: number
  pageSize: number
  searchParams: Record<string, unknown>
  useMilliseconds?: boolean
}): {
  p: number
  page_size: number
  channel_id?: string
  start_timestamp?: number
  end_timestamp?: number
} {
  const { page, pageSize, searchParams, useMilliseconds = false } = config

  return {
    p: page,
    page_size: pageSize,
    ...(searchParams.channel
      ? {
          channel_id: String(searchParams.channel),
        }
      : {}),
    ...buildTimeRangeParams(searchParams, useMilliseconds),
  }
}

/**
 * Build API params from search params and column filters (for common logs)
 */
export function buildApiParams(config: {
  page: number
  pageSize: number
  searchParams: Record<string, unknown>
  columnFilters?: Array<{ id: string; value: unknown }>
  isAdmin: boolean
}): GetLogsParams {
  const { page, pageSize, searchParams, columnFilters = [], isAdmin } = config

  // Helper to process type parameter (single value from array)
  const processType = (value: unknown): number | undefined => {
    const parseType = (raw: unknown): number | undefined => {
      const type = Number(raw)
      return Number.isFinite(type) ? type : undefined
    }

    if (Array.isArray(value) && value.length === 1) {
      return parseType(value[0])
    }
    if (typeof value === 'string' && value !== '') {
      return parseType(value)
    }
    return undefined
  }

  const userId = logUserIdSearchSchema.safeParse(
    isAdmin ? searchParams.userId : undefined
  )
  if (!userId.success) {
    throw createServerError({ message_key: LOG_USER_ID_ERROR })
  }

  // Build base params from search params
  const params: GetLogsParams = {
    p: page,
    page_size: pageSize,
    ...(userId.data ? { user_id: userId.data } : {}),
    ...(searchParams.type ? { type: processType(searchParams.type) } : {}),
    ...(searchParams.model ? { model_name: String(searchParams.model) } : {}),
    ...(searchParams.token ? { token_name: String(searchParams.token) } : {}),
    ...(searchParams.group ? { group: String(searchParams.group) } : {}),
    ...(isAdmin && searchParams.channel
      ? { channel: Number(searchParams.channel) || 0 }
      : {}),
    ...(isAdmin && searchParams.username
      ? { username: String(searchParams.username) }
      : {}),
    ...(searchParams.requestId
      ? { request_id: String(searchParams.requestId) }
      : {}),
    ...(searchParams.upstreamRequestId
      ? { upstream_request_id: String(searchParams.upstreamRequestId) }
      : {}),
    ...buildTimeRangeParams(searchParams, false),
  }

  // Override with column filters if present
  if (columnFilters.length > 0) {
    columnFilters.forEach(({ id, value }) => {
      if (value === undefined || value === null || value === '') return

      switch (id) {
        case 'type':
          params.type = processType(value)
          break
        case 'model_name':
          params.model_name = String(value)
          break
        case 'token_name':
          params.token_name = String(value)
          break
        case 'group':
          params.group = String(value)
          break
        case 'channel':
          if (isAdmin) params.channel = Number(value) || 0
          break
        case 'username':
          if (isAdmin) params.username = String(value)
          break
      }
    })
  }

  return params
}

// ============================================================================
// Data Fetching
// ============================================================================

// A sample belongs to a search, not a page. Implicit "today" must also
// change scope at local midnight without creating a new key every second.
export function logFilterScope(search: Record<string, unknown>) {
  const { page: _page, pageSize: _pageSize, ...filters } = search
  const defaultDay =
    filters.startTime || filters.endTime
      ? undefined
      : getDefaultTimeRange().start.getTime()
  return { filters, defaultDay }
}

export const LOG_SUMMARY_FRESH_MS = 30_000

type LogCountSample = { total: number; endTimestamp?: number }

/**
 * Fetch logs based on category type
 */
export async function fetchLogsByCategory(
  config: FetchLogsConfig,
  countCache?: { client: QueryClient; scope: string }
): Promise<GetLogsResponse> {
  const { logCategory, isAdmin, page, pageSize, searchParams, columnFilters } =
    config

  if (logCategory === 'common') {
    const params = buildApiParams({
      page,
      pageSize,
      searchParams,
      columnFilters,
      isAdmin,
    })
    const fetchPage = isAdmin ? getAllLogs : getUserLogs
    if (!countCache) return fetchPage(params)

    const key = ['usage-log-count', countCache.scope]
    const state = countCache.client.getQueryState<LogCountSample>(key)
    const sample =
      page > 1 &&
      state &&
      !state.isInvalidated &&
      Date.now() - state.dataUpdatedAt < LOG_SUMMARY_FRESH_MS
        ? state.data
        : undefined
    if (sample) {
      // For an implicit "today" range reuse the exact end used for the count.
      const result = await fetchPage({
        ...params,
        skip_total: 1,
        end_timestamp: sample.endTimestamp,
      })
      if (!result.success || !result.data) return result
      if (result.data.total !== -1) return result // older server
      const offset = (page - 1) * pageSize
      const rows = result.data.items.length
      // Recount if newer inserts or retention deletes visibly invalidate the sample.
      const expectedRows = Math.min(
        pageSize,
        Math.max(0, sample.total - offset)
      )
      if (rows === expectedRows) {
        return { ...result, data: { ...result.data, total: sample.total } }
      }
    }
    const result = await fetchPage(params)
    if (result.success && result.data && result.data.total >= 0) {
      countCache.client.setQueryData<LogCountSample>(key, {
        total: result.data.total,
        endTimestamp: params.end_timestamp,
      })
    }
    return result
  }

  // For drawing and task logs
  const baseParams = buildBaseParams({
    page,
    pageSize,
    searchParams,
    useMilliseconds: logCategory === 'drawing',
  })

  const paramsWithFilter = {
    ...baseParams,
    ...(logCategory === 'drawing'
      ? { mj_id: searchParams.filter as string | undefined }
      : {}),
    ...(logCategory === 'task'
      ? { task_id: searchParams.filter as string | undefined }
      : {}),
  }

  if (logCategory === 'drawing') {
    return isAdmin
      ? await getAllMidjourneyLogs(paramsWithFilter as GetMidjourneyLogsParams)
      : await getUserMidjourneyLogs(paramsWithFilter as GetMidjourneyLogsParams)
  }

  // task logs
  return isAdmin
    ? await getAllTaskLogs(paramsWithFilter as GetTaskLogsParams)
    : await getUserTaskLogs(paramsWithFilter as GetTaskLogsParams)
}
