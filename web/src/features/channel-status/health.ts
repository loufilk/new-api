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
import type { TokenGroupDetail } from '@/features/keys/types'

export type GroupHealth = 'healthy' | 'degraded' | 'unknown' | 'unavailable'

export function groupHealth(group: TokenGroupDetail): GroupHealth {
  if (!group.available) return 'unavailable'
  const rate = group.success_rate
  if (rate === null || !Number.isFinite(rate) || rate < 0 || rate > 100) {
    return 'unknown'
  }
  return rate >= 95 ? 'healthy' : 'degraded'
}

export function filterHealthGroups(
  groups: TokenGroupDetail[],
  query: string,
  state: string
): TokenGroupDetail[] {
  const search = query.trim().toLocaleLowerCase()
  return groups.filter((group) => {
    if (state !== 'all' && groupHealth(group) !== state) return false
    return `${group.name} ${group.description ?? ''}`
      .toLocaleLowerCase()
      .includes(search)
  })
}
