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
const MICRO = 1_000_000n

export function formatUSDT(micro: string): string {
  if (!/^-?\d+$/.test(micro)) return '—'
  const value = BigInt(micro)
  const absolute = value < 0 ? -value : value
  const fraction = (absolute % MICRO)
    .toString()
    .padStart(6, '0')
    .replace(/0+$/, '')
  return `${value < 0 ? '-' : ''}${absolute / MICRO}${fraction ? `.${fraction}` : ''}`
}

export function quotaToUSDT(quota: number, quotaPerUSDT: number): string {
  if (
    !Number.isSafeInteger(quota) ||
    !Number.isSafeInteger(quotaPerUSDT) ||
    quotaPerUSDT <= 0
  ) {
    return '—'
  }
  return formatUSDT(((BigInt(quota) * MICRO) / BigInt(quotaPerUSDT)).toString())
}

// The input never goes through floating point multiplication. An amount must
// be representable in both whole quota units and USDT's six decimal places.
export function usdtToQuota(
  amount: string,
  quotaPerUSDT: number
): number | null {
  if (
    !/^\d{1,12}(?:\.\d{1,6})?$/.test(amount) ||
    !Number.isSafeInteger(quotaPerUSDT) ||
    quotaPerUSDT <= 0
  ) {
    return null
  }
  const [whole, fraction = ''] = amount.split('.')
  const micro = BigInt(whole) * MICRO + BigInt(fraction.padEnd(6, '0'))
  const numerator = micro * BigInt(quotaPerUSDT)
  if (numerator % MICRO !== 0n) return null
  const quota = numerator / MICRO
  if (quota <= 0n || quota > BigInt(Number.MAX_SAFE_INTEGER)) return null
  return Number(quota)
}

export function newWithdrawalRequestKey(): string {
  return crypto.randomUUID()
}
