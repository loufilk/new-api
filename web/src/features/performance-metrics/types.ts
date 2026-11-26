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
/** Availability is explicit: a rounded zero can be a real observation. */
type ObservedMeasurements = {
  has_latency?: boolean
  has_ttft?: boolean
  has_tps?: boolean
}

export type PerformanceSummary = ObservedMeasurements & {
  avg_ttft_ms?: number
  avg_latency_ms: number
  success_rate: number
  avg_tps: number
}

export type PerformanceSeriesPoint = ObservedMeasurements & {
  ts: number
  avg_ttft_ms: number
  avg_latency_ms: number
  success_rate: number
  avg_tps: number
}

export type PerformanceGroup = ObservedMeasurements & {
  group: string
  avg_ttft_ms: number
  avg_latency_ms: number
  success_rate: number
  avg_tps: number
  series: PerformanceSeriesPoint[]
}

export type PerformanceMetricsData = {
  success: boolean
  message?: string
  data: {
    summary?: PerformanceSummary | null
    series?: PerformanceSeriesPoint[]
    window_start?: number
    window_end?: number
    model_name: string
    series_schema?: string
    groups: PerformanceGroup[]
  }
}

export type SuccessRatePoint = { ts: number; success_rate: number }

export type PerfModelSummary = PerformanceSummary & {
  model_name: string
  recent_success_series?: SuccessRatePoint[]
  request_count?: number
}

export type PerfSummaryAllData = {
  success: boolean
  message?: string
  data: {
    summary?: PerformanceSummary | null
    window_start?: number
    window_end?: number
    models: PerfModelSummary[]
    recent_success_series?: Record<string, SuccessRatePoint[]>
  }
}
