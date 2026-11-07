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
import type { TestRun } from './types'

export type ModelCapability = 'full' | 'reduced'

export function modelCapability(run: TestRun): ModelCapability | null {
  if (['running', 'cancelled', 'intercepted'].includes(run.status)) return null
  const result = run.fingerprint_result
  if (!result) return null
  if (
    result.status === 'not_model' &&
    result.used_outputs > 0 &&
    result.sample_quality.some((sample) => sample.accepted)
  ) {
    return 'reduced'
  }
  if (
    (result.status === 'scored' || result.status === 'partial') &&
    result.used_outputs > 0 &&
    result.top_candidates.length > 0
  ) {
    return 'full'
  }
  if (
    result.status === 'unscorable' &&
    result.used_outputs === 0 &&
    result.sample_quality.some(
      (sample) => sample.reason === 'insufficient_numbers'
    ) &&
    !result.sample_quality.some(
      (sample) => sample.reason === 'non_model_response'
    )
  ) {
    return 'reduced'
  }
  return null
}

export function latestModelTestRuns(runs: TestRun[]): TestRun[] {
  return [...runs]
    .sort((a, b) => a.created_at - b.created_at || a.id.localeCompare(b.id))
    .slice(-60)
}

export function effectiveFingerprintRuns(runs: TestRun[]): TestRun[] {
  return latestModelTestRuns(
    runs.filter((run) => modelCapability(run) !== null)
  )
}
