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
/** Mirrors the server's reviewed original-money currency catalog. Never infer decimals from a site display currency. */
export const CURRENCY_EXPONENTS: Readonly<Record<string, number>> = {
  USD: 2,
  CNY: 2,
  EUR: 2,
  GBP: 2,
  HKD: 2,
  CAD: 2,
  AUD: 2,
  SGD: 2,
  NZD: 2,
  CHF: 2,
  JPY: 0,
  KRW: 0,
  BHD: 3,
  JOD: 3,
  KWD: 3,
  OMR: 3,
  TND: 3,
}
export function currencyOptions(exponentField?: string) {
  return Object.entries(CURRENCY_EXPONENTS).map(([currency, exponent]) => ({
    value: currency,
    label: currency,
    updates: exponentField ? { [exponentField]: String(exponent) } : undefined,
  }))
}
export function validCurrencyExponent(
  currency: string,
  exponent: number
): boolean {
  return (
    Object.hasOwn(CURRENCY_EXPONENTS, currency) &&
    CURRENCY_EXPONENTS[currency] === exponent
  )
}
