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
/** All budget arithmetic runs in integer quota units, never display currency. */
export function getBatchQuota(quota: number, count: number): number | null {
  if (
    !Number.isSafeInteger(quota) ||
    quota <= 0 ||
    !Number.isSafeInteger(count) ||
    count < 1 ||
    count > 100
  ) {
    return null
  }
  const total = quota * count
  return Number.isSafeInteger(total) ? total : null
}

export function canReserveQuota(
  quota: number,
  count: number,
  available: number
): boolean {
  const total = getBatchQuota(quota, count)
  return total !== null && Number.isSafeInteger(available) && available >= total
}

export function canAllocateQuota(delta: number, available: number): boolean {
  return (
    Number.isSafeInteger(delta) &&
    delta !== 0 &&
    Number.isSafeInteger(available) &&
    available >= 0 &&
    Number.isSafeInteger(available + delta) &&
    available + delta >= 0
  )
}
