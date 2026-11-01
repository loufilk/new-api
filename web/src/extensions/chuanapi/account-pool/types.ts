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
export type PoolStatus =
  | 'available'
  | 'rate_limited'
  | 'unavailable'
  | 'paused'
  | 'error'
  | 'unknown'
export type PoolGroup = { name: string; ratio: number }
export type PoolAccount = {
  anonymous_id: string
  provider: string
  auth_type: string
  plan: string
  status: PoolStatus
  concurrency: number | null
  concurrency_limit: number | null
  groups: PoolGroup[]
  usage_5h_percent: number | null
  usage_7d_percent: number | null
  last_used_at: number | null
}
export type PoolGroupSummary = PoolGroup & {
  total: number
  available: number
  concurrency: number | null
  concurrency_limit: number | null
}
export type PoolSnapshot = {
  configured: boolean
  updated_at: number
  state: 'unconfigured' | 'ready' | 'stale' | 'unavailable'
  items: PoolAccount[]
  total: number
  groups: PoolGroupSummary[]
}
export type PoolFilters = {
  q: string
  type: string
  status: string
  group: string
  page: number
  page_size: number
}
