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
import * as z from 'zod'

const invalidDisplayCharacters = /[\p{Cc}\p{Cf}\p{Cs}]/u

export const publicModelOwnerSchema = z
  .string()
  .trim()
  .refine(
    (value) =>
      [...value].length <= 128 && !invalidDisplayCharacters.test(value),
    'Public model owner must be at most 128 characters and contain no control or formatting characters'
  )
  .transform((value) => value || 'chuanapi')

export function normalizeSensitiveExemptGroups(value: string): string {
  return [
    ...new Set(
      value
        .split('\n')
        .map((group) => group.trim())
        .filter(Boolean)
    ),
  ].join('\n')
}

export const sensitiveExemptGroupsSchema = z
  .string()
  .refine(
    (value) =>
      normalizeSensitiveExemptGroups(value)
        .split('\n')
        .every(
          (group) =>
            group !== 'auto' &&
            group !== '*' &&
            [...group].length <= 128 &&
            !invalidDisplayCharacters.test(group)
        ),
    'Filtering exemptions require exact group names, one per line; auto and wildcards are not allowed'
  )
  .transform(normalizeSensitiveExemptGroups)
