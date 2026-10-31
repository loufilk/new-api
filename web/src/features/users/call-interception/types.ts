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
export type UserCallInterception = {
  user_id: number
  enabled: boolean
  reply: string
  revision: number
  updated_at: number
  updated_by: number
}

export type CallInterceptionUpdate = {
  enabled: boolean
  reply: string
  expected_revision: number
}

export type InterceptionHit = {
  id: number
  time: number
  user_id: number
  token_id: number
  actor_user_id: number
  identity_kind: string
  request_id: string
  interface: string
  protocol: string
  status: number
  revision: number
}

export type InterceptionAudit = {
  id: number
  time: number
  user_id: number
  actor_id: number
  request_id: string
  previous_enabled: boolean
  enabled: boolean
  reply_changed: boolean
  revision: number
}

export type InterceptionLog = InterceptionHit | InterceptionAudit
export type InterceptionLogKind = 'hits' | 'audits'
export type InterceptionLogPage = {
  items: InterceptionLog[]
  total: number
  page: number
  page_size: number
}
export type InterceptionLogFilters = {
  p: number
  page_size: number
  request_id?: string
  token_id?: number
  actor_id?: number
  actor_user_id?: number
  protocol?: string
  identity_kind?: string
  status?: number
  start_time?: number
  end_time?: number
}
