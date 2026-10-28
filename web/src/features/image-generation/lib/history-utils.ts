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
import { IMAGE_HISTORY_MAX_ENTRIES, IMAGE_HISTORY_TTL_MS } from '../constants'
import type { ImageHistoryEntry } from '../types'
import { isBlobSchemeUrl, isHttpSchemeUrl } from './managed-object-urls'
import { getImageOrientation } from './model-capabilities'

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function isFinitePositiveNumber(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0
}

function isNonEmptyString(value: unknown): value is string {
  return typeof value === 'string' && value.length > 0
}

function isRemoteImageSource(value: string): boolean {
  if (!isHttpSchemeUrl(value)) return false
  return /^https?:\/\//i.test(value.trimStart())
}

function isBase64ImageSource(value: string): boolean {
  return /^data:image\/(?:png|jpe?g|webp);base64,/i.test(value)
}

function isTemporaryObjectUrl(value: string): boolean {
  return isBlobSchemeUrl(value)
}

export function dataUrlToBlob(dataUrl: string): Blob | null {
  const match = /^data:(image\/(?:png|jpe?g|webp));base64,(.*)$/is.exec(
    dataUrl.replaceAll(/\s+/g, '')
  )
  if (!match) return null
  const mime = match[1].toLowerCase()
  const base64 = match[2]
  if (!base64) return null
  try {
    const binary = atob(base64)
    const bytes = new Uint8Array(binary.length)
    for (let index = 0; index < binary.length; index += 1) {
      bytes[index] = binary.charCodeAt(index)
    }
    return new Blob([bytes], { type: mime })
  } catch {
    return null
  }
}

function isValidResult(value: unknown): boolean {
  if (!isRecord(value)) return false
  if (typeof value.id !== 'string' || !isNonEmptyString(value.source)) {
    return false
  }
  if (value.type !== 'url' && value.type !== 'b64' && value.type !== 'blob') {
    return false
  }
  if (
    typeof value.createdAt !== 'number' ||
    !Number.isFinite(value.createdAt)
  ) {
    return false
  }
  if (typeof value.keptLocally !== 'boolean') return false
  if (value.width !== undefined && !isFinitePositiveNumber(value.width)) {
    return false
  }
  if (value.height !== undefined && !isFinitePositiveNumber(value.height)) {
    return false
  }
  if (
    value.dimensionsVerified !== undefined &&
    value.dimensionsVerified !== true
  ) {
    return false
  }
  if (
    value.dimensionsVerified &&
    (!isFinitePositiveNumber(value.width) ||
      !isFinitePositiveNumber(value.height))
  ) {
    return false
  }
  if (value.type === 'blob') {
    if (value.keptLocally !== true) return false
    if (!isNonEmptyString(value.blobKey)) return false
    if (value.source !== value.blobKey) return false
  } else {
    if (value.keptLocally) return false
    if (value.type === 'url') {
      if (
        !isRemoteImageSource(value.source) &&
        !isTemporaryObjectUrl(value.source)
      ) {
        return false
      }
    }
    if (value.type === 'b64') {
      if (
        !isBase64ImageSource(value.source) &&
        !isTemporaryObjectUrl(value.source)
      ) {
        return false
      }
    }
  }
  return true
}

export function isValidHistoryEntry(
  value: unknown
): value is ImageHistoryEntry {
  if (!isRecord(value)) return false
  if (typeof value.id !== 'string' || typeof value.prompt !== 'string') {
    return false
  }
  if (typeof value.model !== 'string') return false
  if (
    typeof value.createdAt !== 'number' ||
    !Number.isFinite(value.createdAt)
  ) {
    return false
  }
  if (!isRecord(value.settings)) return false
  const settings = value.settings
  if (
    typeof settings.size !== 'string' ||
    typeof settings.quality !== 'string' ||
    typeof settings.style !== 'string'
  ) {
    return false
  }
  if (!isFinitePositiveNumber(settings.count)) return false
  if (!Array.isArray(value.results)) return false
  return value.results.every(isValidResult)
}

export function pruneHistoryEntries(
  entries: ImageHistoryEntry[],
  now: number = Date.now()
): ImageHistoryEntry[] {
  const fresh: ImageHistoryEntry[] = []
  for (const entry of entries) {
    if (!isValidHistoryEntry(entry)) continue
    const keptResults = entry.results.filter((result) => {
      if (result.keptLocally || result.type === 'blob') return true
      return now - result.createdAt < IMAGE_HISTORY_TTL_MS
    })
    if (keptResults.length === 0) continue
    if (
      now - entry.createdAt >= IMAGE_HISTORY_TTL_MS &&
      !keptResults.some(
        (result) => result.keptLocally || result.type === 'blob'
      )
    ) {
      continue
    }
    fresh.push({ ...entry, results: keptResults })
  }
  return fresh
    .sort((a, b) => b.createdAt - a.createdAt)
    .slice(0, IMAGE_HISTORY_MAX_ENTRIES)
}

export function filterHistoryEntries(
  entries: ImageHistoryEntry[],
  query: string,
  orientation: 'all' | 'square' | 'landscape' | 'portrait'
): ImageHistoryEntry[] {
  const keyword = query.trim().toLowerCase()
  return entries.filter((entry) => {
    if (keyword !== '') {
      const haystack = `${entry.prompt}\n${entry.model}`.toLowerCase()
      if (!haystack.includes(keyword)) {
        return false
      }
    }
    if (orientation === 'all') return true
    return entry.results.some((result) => {
      if (!result.dimensionsVerified) return false
      return getImageOrientation(result.width, result.height) === orientation
    })
  })
}

export function resolveEntryOrientation(
  entry: ImageHistoryEntry
): 'square' | 'landscape' | 'portrait' | 'unknown' {
  for (const result of entry.results) {
    if (!result.dimensionsVerified) continue
    const orientation = getImageOrientation(result.width, result.height)
    if (orientation !== 'unknown') return orientation
  }
  return 'unknown'
}
