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
export const IMAGE_HISTORY_STORAGE_KEY = 'image-generation-history:v1'
export const IMAGE_HISTORY_MAX_ENTRIES = 120
export const IMAGE_HISTORY_TTL_MS = 60 * 60 * 1000
export const IMAGE_BLOB_STORE_NAME = 'image-generation-store'

export const IMAGE_REFERENCE_MAX_TOTAL_BYTES = 15 * 1024 * 1024
export const IMAGE_REFERENCE_ALLOWED_MIME = [
  'image/png',
  'image/jpeg',
  'image/webp',
] as const

export const IMAGE_COUNT_MIN = 1
export const IMAGE_COUNT_MAX = 4

export const IMAGE_PROMPT_EXAMPLE_KEYS = [
  'A tranquil lake at sunrise, soft mist, photorealistic',
  'A cozy robot reading in a library, warm light, illustration',
  'Minimalist mountain poster, bold shapes, flat design',
  'A futuristic city street at night, neon reflections',
] as const
