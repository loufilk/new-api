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
export const SUPPORT_IMAGE_LIMIT = 4
export const SUPPORT_IMAGE_MAX_BYTES = 5 * 1024 * 1024
export const SUPPORT_IMAGE_TYPES = ['image/png', 'image/jpeg', 'image/webp']

export function supportImageError(
  files: readonly File[],
  existingCount = 0
): string | null {
  if (files.length + existingCount > SUPPORT_IMAGE_LIMIT) {
    return 'Attach at most 4 images per message.'
  }
  if (files.some((file) => !SUPPORT_IMAGE_TYPES.includes(file.type))) {
    return 'Only PNG, JPEG and WebP images are supported.'
  }
  if (
    files.some((file) => file.size <= 0 || file.size > SUPPORT_IMAGE_MAX_BYTES)
  ) {
    return 'Each image must be nonempty and no larger than 5 MB.'
  }
  return null
}
