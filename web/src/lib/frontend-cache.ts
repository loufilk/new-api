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
const FRONTEND_CACHE_VERSION = 'default-v1'
const FRONTEND_CACHE_VERSION_KEY = 'newapi:default:cache-version'
// Only discard caches that can be fetched again. User-owned conversations,
// image history, preferences and other same-origin applications are not caches.
const REFETCHABLE_LOCAL_STORAGE_KEYS = [
  'status',
  'status:updatedAt',
  'home_page_content',
]

export function initializeFrontendCache(): void {
  if (typeof window === 'undefined') return

  try {
    const currentVersion = window.localStorage.getItem(
      FRONTEND_CACHE_VERSION_KEY
    )
    if (currentVersion === FRONTEND_CACHE_VERSION) return

    clearLocalUiCache()
    window.localStorage.setItem(
      FRONTEND_CACHE_VERSION_KEY,
      FRONTEND_CACHE_VERSION
    )
  } catch {
    // Storage can be unavailable in private mode; the app should still boot.
  }
}

function clearLocalUiCache(): void {
  REFETCHABLE_LOCAL_STORAGE_KEYS.forEach((key) =>
    window.localStorage.removeItem(key)
  )
}
