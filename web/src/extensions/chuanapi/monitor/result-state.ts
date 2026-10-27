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
import type { MonitorResult } from './types'

export function resultStatusKey(result: MonitorResult): string {
  if (result.status === 'ready') {
    if (result.quality_status === 'passed') return 'monitor.passed'
    if (result.quality_status === 'failed') return 'monitor.notPassed'
    if (result.quality_status === 'suspicious') {
      return 'monitor.needsVerification'
    }
    return 'monitor.awaitingReview'
  }
  switch (result.status) {
    case 'passed':
      return 'monitor.passed'
    case 'incorrect':
      return 'monitor.notPassed'
    case 'running':
      return 'monitor.running'
    case 'queued':
      return 'monitor.queued'
    case 'failed':
    case 'interrupted':
      return 'monitor.requestFailed'
    case 'review':
      return 'monitor.awaitingReview'
    default:
      return 'monitor.unknown'
  }
}

export function resultTime(
  timestamp: number,
  locale: string | undefined
): string {
  if (!Number.isFinite(timestamp) || timestamp <= 0) return '—'
  const date = new Date(timestamp * 1000)
  if (!Number.isFinite(date.getTime())) return '—'
  return new Intl.DateTimeFormat(locale, {
    timeZone: 'Asia/Shanghai',
    timeZoneName: 'short',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(date)
}
