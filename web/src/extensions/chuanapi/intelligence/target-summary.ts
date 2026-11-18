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
import { modelCapability } from './timeline'
import type { TestRun } from './types'

export type QualityTarget = { group: string; model: string }
export type QualityTargetSummary<T extends QualityTarget> = T & {
  capability: 'full' | 'reduced' | null
}

export function summarizeQualityTargets<T extends QualityTarget>(
  targets: T[],
  runs: TestRun[]
): QualityTargetSummary<T>[] {
  const latest = new Map<string, TestRun>()
  for (const run of runs) {
    if (modelCapability(run) === null) continue
    const key = JSON.stringify([run.group, run.model])
    const previous = latest.get(key)
    if (!previous || run.created_at > previous.created_at) latest.set(key, run)
  }
  return targets.map((target) => {
    const result = latest.get(JSON.stringify([target.group, target.model]))
    return { ...target, capability: result ? modelCapability(result) : null }
  })
}

export function filterQualityTargets<T extends QualityTarget>(
  targets: QualityTargetSummary<T>[],
  search: string,
  state: string
): QualityTargetSummary<T>[] {
  const query = search.trim().toLocaleLowerCase()
  return targets.filter((target) => {
    if (state === 'unknown' && target.capability !== null) return false
    if (state === 'full' && target.capability !== 'full') return false
    if (state === 'reduced' && target.capability !== 'reduced') return false
    return `${target.group} ${target.model}`.toLocaleLowerCase().includes(query)
  })
}
