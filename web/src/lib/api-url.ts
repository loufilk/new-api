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
/**
 * Single source of truth for the API origin.
 *
 * `VITE_API_BASE_URL` is injected by rsbuild (see `rsbuild.config.ts`). The
 * default is an empty string, which keeps every app-owned request on the
 * frontend origin and preserves the existing same-origin dev proxy behavior.
 *
 * Only app-owned paths go through `apiUrl`. Absolute third-party URLs (image
 * CDNs, plugin indexes, OAuth endpoints, ...) must pass through untouched, and
 * third-party requests must never receive console credentials.
 */
const rawApiBaseUrl =
  typeof import.meta.env.VITE_API_BASE_URL === 'string'
    ? import.meta.env.VITE_API_BASE_URL.trim()
    : ''

/** Normalized API origin; may include a path prefix. Empty means same-origin. */
export const API_BASE_URL = rawApiBaseUrl.replace(/\/+$/, '')

const ABSOLUTE_URL_PATTERN = /^(?:[a-z][a-z0-9+.-]*:|\/\/)/i

/** Whether the console talks to an explicitly configured API origin. */
export function hasExternalApiBaseUrl(): boolean {
  return API_BASE_URL !== ''
}

/**
 * Resolve an app-owned request path against the configured API origin.
 * Absolute URLs are returned unchanged.
 */
export function apiUrl(path: string): string {
  if (!API_BASE_URL || ABSOLUTE_URL_PATTERN.test(path)) return path
  return `${API_BASE_URL}${path.startsWith('/') ? '' : '/'}${path}`
}

/**
 * Resolve an app-relative resource link (task artifacts, generated images)
 * against the API origin while leaving third-party absolute URLs alone.
 */
export function apiResourceUrl(url: string): string {
  if (!url) return url
  return apiUrl(url)
}
