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
import type { GroupRenameMapping } from '../types'
import { safeJsonParse } from '../utils/json-parser'

export type GroupVisibilityConflict = {
  userGroup: string
  targetGroup: string
}

export function collectGroupVisibilityConflicts(
  raw: string,
  renames: GroupRenameMapping[] = []
): GroupVisibilityConflict[] {
  const parsed = safeJsonParse<unknown>(raw, { fallback: {}, silent: true })
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return []

  const renamedGroups = new Map(renames.map(({ from, to }) => [from, to]))
  const visibilityByGroup = new Map<
    string,
    { visible: Set<string>; hidden: Set<string> }
  >()
  for (const [userGroup, rules] of Object.entries(parsed)) {
    if (!rules || typeof rules !== 'object' || Array.isArray(rules)) continue
    const renamedUserGroup = renamedGroups.get(userGroup) ?? userGroup
    let visibility = visibilityByGroup.get(renamedUserGroup)
    if (!visibility) {
      visibility = { visible: new Set(), hidden: new Set() }
      visibilityByGroup.set(renamedUserGroup, visibility)
    }
    for (const key of Object.keys(rules)) {
      const hidden = key.startsWith('-:')
      const target = hidden || key.startsWith('+:') ? key.slice(2) : key
      const renamedTarget = renamedGroups.get(target) ?? target
      const targets = hidden ? visibility.hidden : visibility.visible
      targets.add(renamedTarget)
    }
  }

  const conflicts: GroupVisibilityConflict[] = []
  for (const [userGroup, visibility] of visibilityByGroup) {
    for (const targetGroup of visibility.hidden) {
      if (visibility.visible.has(targetGroup)) {
        conflicts.push({ userGroup, targetGroup })
      }
    }
  }
  return conflicts
}
