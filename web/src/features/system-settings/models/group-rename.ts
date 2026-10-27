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

export type RenamableGroupRow = {
  originalName: string | null
  name: string
}

export type GroupRatioSaveOutcome = 'saved' | 'partial' | 'failed'

export const GROUP_RENAME_FIELDS = [
  'GroupRatio',
  'TopupGroupRatio',
  'UserUsableGroups',
  'GroupGroupRatio',
  'AutoGroups',
  'GroupSpecialUsableGroup',
] as const

export type GroupRenameField = (typeof GROUP_RENAME_FIELDS)[number]

export type GroupRenameEditableValues = {
  GroupRatio: string
  TopupGroupRatio: string
  UserUsableGroups: string
  GroupGroupRatio: string
  AutoGroups: string
  GroupSpecialUsableGroup: string
}

// Keep in sync with reservedGroupNames in model/group_rename.go: these names
// carry runtime meaning and must never be used as a rename source or target.
export const RESERVED_GROUP_NAMES = new Set(['default', 'auto', 'all'])

export function isReservedGroupName(name: string): boolean {
  return RESERVED_GROUP_NAMES.has(name)
}

export function collectGroupRenames(
  rows: RenamableGroupRow[]
): GroupRenameMapping[] {
  const renames: GroupRenameMapping[] = []
  for (const row of rows) {
    const target = row.name.trim()
    if (!row.originalName || !target || target === row.originalName) {
      continue
    }
    if (isReservedGroupName(row.originalName) || isReservedGroupName(target)) {
      continue
    }
    renames.push({ from: row.originalName, to: target })
  }
  return renames
}

function renameGroupName(name: string, lookup: Map<string, string>): string {
  return lookup.get(name) ?? name
}

function renameSpecialUsableKey(
  rawKey: string,
  lookup: Map<string, string>
): string {
  if (rawKey.startsWith('-:')) {
    return `-:${renameGroupName(rawKey.slice(2), lookup)}`
  }
  if (rawKey.startsWith('+:')) {
    return `+:${renameGroupName(rawKey.slice(2), lookup)}`
  }
  return renameGroupName(rawKey, lookup)
}

function renameKeyedValues(raw: string, lookup: Map<string, string>): string {
  const values = safeJsonParse<Record<string, unknown>>(raw, {
    fallback: {},
    silent: true,
  })
  const renamed: Record<string, unknown> = {}
  for (const [name, value] of Object.entries(values)) {
    renamed[renameGroupName(name, lookup)] = value
  }
  return JSON.stringify(renamed, null, 2)
}

export function applyGroupRenamesToValues<T extends GroupRenameEditableValues>(
  values: T,
  renames: GroupRenameMapping[]
): T {
  if (renames.length === 0) {
    return values
  }
  const lookup = new Map(renames.map((rename) => [rename.from, rename.to]))

  const nested = safeJsonParse<Record<string, Record<string, number>>>(
    values.GroupGroupRatio,
    { fallback: {}, silent: true }
  )
  const renamedNested: Record<string, Record<string, number>> = {}
  for (const [userGroup, overrides] of Object.entries(nested)) {
    const renamedOverrides: Record<string, number> = {}
    for (const [targetGroup, ratio] of Object.entries(overrides ?? {})) {
      renamedOverrides[renameGroupName(targetGroup, lookup)] = ratio
    }
    renamedNested[renameGroupName(userGroup, lookup)] = renamedOverrides
  }

  const autoGroups = safeJsonParse<string[]>(values.AutoGroups, {
    fallback: [],
    silent: true,
  })
  const renamedAutoGroups = autoGroups.map((group) =>
    renameGroupName(group, lookup)
  )

  const specialUsable = safeJsonParse<Record<string, Record<string, string>>>(
    values.GroupSpecialUsableGroup,
    { fallback: {}, silent: true }
  )
  const renamedSpecialUsable: Record<string, Record<string, string>> = {}
  for (const [userGroup, rules] of Object.entries(specialUsable)) {
    const renamedRules: Record<string, string> = {}
    for (const [rawKey, description] of Object.entries(rules ?? {})) {
      renamedRules[renameSpecialUsableKey(rawKey, lookup)] = description
    }
    renamedSpecialUsable[renameGroupName(userGroup, lookup)] = renamedRules
  }

  return {
    ...values,
    GroupRatio: renameKeyedValues(values.GroupRatio, lookup),
    TopupGroupRatio: renameKeyedValues(values.TopupGroupRatio, lookup),
    UserUsableGroups: renameKeyedValues(values.UserUsableGroups, lookup),
    GroupGroupRatio: JSON.stringify(renamedNested, null, 2),
    AutoGroups: JSON.stringify(renamedAutoGroups, null, 2),
    GroupSpecialUsableGroup: JSON.stringify(renamedSpecialUsable, null, 2),
  }
}

export function parseGroupOptionKeys(
  field: GroupRenameField,
  raw: string
): string[] {
  switch (field) {
    case 'GroupRatio':
    case 'TopupGroupRatio':
    case 'UserUsableGroups':
      return Object.keys(
        safeJsonParse<Record<string, unknown>>(raw, {
          fallback: {},
          silent: true,
        })
      )
    case 'AutoGroups':
      return safeJsonParse<string[]>(raw, { fallback: [], silent: true })
    case 'GroupGroupRatio': {
      const nested = safeJsonParse<Record<string, Record<string, number>>>(
        raw,
        { fallback: {}, silent: true }
      )
      return [
        ...new Set(
          Object.entries(nested).flatMap(([userGroup, overrides]) => [
            userGroup,
            ...Object.keys(overrides ?? {}),
          ])
        ),
      ]
    }
    case 'GroupSpecialUsableGroup': {
      const nested = safeJsonParse<Record<string, Record<string, string>>>(
        raw,
        { fallback: {}, silent: true }
      )
      const keys: string[] = []
      for (const [userGroup, rules] of Object.entries(nested)) {
        keys.push(userGroup)
        for (const rawKey of Object.keys(rules ?? {})) {
          keys.push(rawKey.replace(/^[-+]:/, ''))
        }
      }
      return [...new Set(keys)]
    }
  }
}

export type GroupRenameIssue = {
  field: GroupRenameField
  key: string
  targets: string[]
}

export type GroupRenamePlan = {
  unresolved: GroupRenameIssue[]
  reservedViolations: string[]
}

export type GroupRenameResolutions = Record<string, string>

export function groupRenameResolutionKey(key: string): string {
  return key
}

export function collectResolutionMappings(
  resolutions: GroupRenameResolutions
): GroupRenameMapping[] {
  return Object.entries(resolutions)
    .filter(([from, target]) => target && target !== from)
    .map(([from, target]) => ({ from, to: target }))
}

export function mergeGroupRenameMappings(
  renames: GroupRenameMapping[],
  extra: GroupRenameMapping[]
): GroupRenameMapping[] {
  const merged = new Map<string, string>()
  for (const rename of renames) {
    merged.set(rename.from, rename.to)
  }
  for (const rename of extra) {
    merged.set(rename.from, rename.to)
  }
  return [...merged.entries()].map(([from, to]) => ({ from, to }))
}

/**
 * Only GroupRatio defines groups. Other settings hold pricing, visibility, or
 * routing references, so removing those references must not rename a group.
 * Definition replacements need an explicit mapping; plain definition removals
 * remain subject to the server's reference check.
 */
export function collectGroupRenamePlan(
  baseline: GroupRenameEditableValues,
  submitted: GroupRenameEditableValues,
  renames: GroupRenameMapping[],
  resolutions: GroupRenameResolutions
): GroupRenamePlan {
  const mappingLookup = new Map(
    renames.map((rename) => [rename.from, rename.to])
  )
  const reservedViolations: string[] = []
  const unresolved: GroupRenameIssue[] = []
  const before = new Set(
    parseGroupOptionKeys('GroupRatio', baseline.GroupRatio)
  )
  const after = new Set(
    parseGroupOptionKeys('GroupRatio', submitted.GroupRatio)
  )
  const added = [...after]
    .filter((key) => !before.has(key) && !isReservedGroupName(key))
    .sort()

  for (const key of before) {
    if (after.has(key)) continue
    if (isReservedGroupName(key)) {
      reservedViolations.push(key)
      continue
    }
    if (mappingLookup.has(key) || added.length === 0) continue
    const resolution = resolutions[groupRenameResolutionKey(key)]
    if (resolution && added.includes(resolution)) continue
    unresolved.push({ field: 'GroupRatio', key, targets: added })
  }

  return { unresolved, reservedViolations: reservedViolations.sort() }
}
