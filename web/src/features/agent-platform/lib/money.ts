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
/** Fixed-source money is not converted using the site's display exchange rate. */
import { getCurrencyDisplay } from '@/lib/currency'

export function formatAgentCash(quota: number): string {
  const amount = (quota / getCurrencyDisplay().config.quotaPerUnit)
    .toFixed(6)
    .replace(/(\.\d{2,}?)0+$/, '$1')
  return `¥${amount}`
}

/** A gateway payment must cover the whole debt even when it includes sub-fen units. */
export function agentRepaymentAmount(quota: number): string {
  const unit = getCurrencyDisplay().config.quotaPerUnit
  if (
    !Number.isSafeInteger(quota) ||
    quota <= 0 ||
    !Number.isSafeInteger(unit) ||
    unit <= 0
  ) {
    return ''
  }
  const numerator = BigInt(quota) * 100n
  const fen = (numerator + BigInt(unit) - 1n) / BigInt(unit)
  return minorMoneyInput(fen.toString(), 2)
}

export function formatAgentQuota(
  quota: number,
  quotaPerUnit = getCurrencyDisplay().config.quotaPerUnit
): string {
  const unit =
    quotaPerUnit > 0 ? quotaPerUnit : getCurrencyDisplay().config.quotaPerUnit
  return `$${(quota / unit).toLocaleString('en-US', { maximumFractionDigits: 2 })}`
}

/** A07 face values and CNY cost units do not use the wallet display exchange rate. */
export function parseAgentQuota(value: string): number | null {
  if (!/^-?\d+(?:\.\d{1,2})?$/.test(value)) return null
  const unit = getCurrencyDisplay().config.quotaPerUnit
  if (!Number.isSafeInteger(unit) || unit <= 0) return null
  const negative = value.startsWith('-')
  const [whole, fraction = ''] = value.replace(/^-/, '').split('.')
  const numerator =
    (BigInt(whole) * 100n + BigInt(fraction.padEnd(2, '0'))) * BigInt(unit)
  if (numerator % 100n || numerator / 100n > BigInt(Number.MAX_SAFE_INTEGER)) {
    return null
  }
  const quota = Number(numerator / 100n)
  return negative ? -quota : quota
}

export function formatMinorMoney(
  minor: string,
  currency: string,
  exponent: number,
  locale: string | undefined
): string {
  if (
    !/^-?(0|[1-9]\d*)$/.test(minor) ||
    !/^[A-Z]{3}$/.test(currency) ||
    !Number.isInteger(exponent) ||
    exponent < 0 ||
    exponent > 6
  ) {
    return '—'
  }
  const amount = BigInt(minor)
  const factor = 10n ** BigInt(exponent)
  const whole = amount / factor
  const remainder = (amount < 0n ? -amount : amount) % factor
  const number = new Intl.NumberFormat(locale, {
    style: 'currency',
    currency,
    currencyDisplay: 'code',
    minimumFractionDigits: exponent,
    maximumFractionDigits: exponent,
  })
  const fraction = new Intl.NumberFormat(locale, {
    useGrouping: false,
    minimumIntegerDigits: Math.max(1, exponent),
    maximumFractionDigits: 0,
  }).format(remainder)
  return number
    .formatToParts(amount < 0n && whole === 0n ? -0 : whole)
    .map((part) => (part.type === 'fraction' ? fraction : part.value))
    .join('')
}

/** Parse decimal text without binary floating point or silent rounding. */
export function parseMinorMoney(
  value: string,
  exponent: number
): string | null {
  if (
    !Number.isInteger(exponent) ||
    exponent < 0 ||
    exponent > 6 ||
    !/^\d+(?:\.\d+)?$/.test(value)
  ) {
    return null
  }
  const [whole, fraction = ''] = value.split('.')
  if (fraction.length > exponent) return null
  const result =
    BigInt(whole) * 10n ** BigInt(exponent) +
    BigInt(fraction.padEnd(exponent, '0') || '0')
  if (result <= 0n || result > 9223372036854775807n) return null
  return result.toString()
}

export function minorMoneyInput(minor: string, exponent: number): string {
  if (
    !/^(0|[1-9]\d*)$/.test(minor) ||
    !Number.isInteger(exponent) ||
    exponent < 0 ||
    exponent > 6
  ) {
    return ''
  }
  if (exponent === 0) return minor
  const padded = minor.padStart(exponent + 1, '0')
  return `${padded.slice(0, -exponent)}.${padded.slice(-exponent)}`
}
