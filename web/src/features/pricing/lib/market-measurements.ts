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
import {
  formatLatency,
  formatThroughput,
  formatUptimePct,
} from '@/features/performance-metrics/lib/format'
import type { PerformanceSummary } from '@/features/performance-metrics/types'

/** Use only the corresponding wire measurement. Missing data is not a zero sample. */
export function marketMeasurements(sample?: PerformanceSummary | null) {
  const latency = sample?.avg_latency_ms
  const ttft = sample?.avg_ttft_ms
  const tps = sample?.avg_tps
  const success = sample?.success_rate
  return {
    latency: measured(sample?.has_latency, latency, formatLatency, '0ms'),
    ttft: measured(sample?.has_ttft, ttft, formatLatency, '0ms'),
    tps: measured(sample?.has_tps, tps, formatThroughput, '0 t/s'),
    success:
      validMeasurement(success) && success <= 100
        ? formatUptimePct(success)
        : null,
  }
}

function validMeasurement(value?: number): value is number {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0
}

function measured(
  present: boolean | undefined,
  value: number | undefined,
  formatter: (n: number) => string,
  zero: string
) {
  if (!present || !validMeasurement(value)) return null
  return value === 0 ? zero : formatter(value)
}
