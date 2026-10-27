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
export type MonitorTarget = {
  id: string
  number: number
  model: string
  name?: string
}
export type MonitorConfig = {
  enabled: boolean
  title: string
  description: string
  groups: Record<string, string>
  hidden_groups: Record<string, boolean>
}
export type MonitorSettingsData = {
  config: MonitorConfig
  environment_enabled: boolean
}
export type MonitorResult = {
  id: string
  target_id: string
  benchmark: 'pelican' | 'candy'
  status: string
  started_at: number
  completed_at: number
  duration_ms: number
  revision: number
  expected_answer?: number
  quality_status?: string
  quality_reason?: string
}
export type MonitorSnapshot = {
  enabled: boolean
  title?: string
  description?: string
  running: boolean
  observed_at: number
  fetched_at: number
  stale: boolean
  targets: MonitorTarget[]
  results: MonitorResult[]
}
export type MonitorDetail = {
  prompt: string
  answer: string
  reference_reason: string
}
export type MonitorResponse<T> = {
  success: boolean
  data: T
  stale?: boolean
  fetched_at?: number
}
