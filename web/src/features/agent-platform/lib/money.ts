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
