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
const STORAGE_KEY = 'image-generation-selected-key:v1'

export function loadImageKeyPreference(ownerId: number | null): number | null {
  if (!Number.isSafeInteger(ownerId) || Number(ownerId) <= 0) return null
  try {
    const id = Number(
      window.localStorage.getItem(`${STORAGE_KEY}:user:${ownerId}`)
    )
    return Number.isSafeInteger(id) && id > 0 ? id : null
  } catch {
    return null
  }
}

export function saveImageKeyPreference(
  ownerId: number | null,
  keyId: number
): void {
  if (!Number.isSafeInteger(ownerId) || Number(ownerId) <= 0) return
  if (!Number.isSafeInteger(keyId) || keyId <= 0) return
  try {
    // Persist only an owned key ID, never its secret or routing configuration.
    window.localStorage.setItem(`${STORAGE_KEY}:user:${ownerId}`, String(keyId))
  } catch {
    // Storage restrictions must not block the selection for this page.
  }
}
