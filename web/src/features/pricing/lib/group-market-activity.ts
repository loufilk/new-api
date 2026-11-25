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
import type {
  GroupMarketActivity,
  MarketActivityByGroup,
  MarketRateChange,
  PricingData,
} from '../types'

// The API uses positive Unix seconds (not milliseconds) at market-event times.
// Keep bad legacy/partial payloads out of date formatting; never invent a time.
export function validMarketTime(seconds: unknown): seconds is number {
  return (
    typeof seconds === 'number' &&
    Number.isSafeInteger(seconds) &&
    seconds > 0 &&
    seconds <= 253402300799
  )
}

export function validMarketRateChange(
  change?: MarketRateChange | null
): change is MarketRateChange {
  return (
    !!change &&
    validMarketTime(change.effective_at) &&
    Number.isFinite(change.old_ratio) &&
    change.old_ratio >= 0 &&
    Number.isFinite(change.new_ratio) &&
    change.new_ratio >= 0 &&
    change.old_ratio !== change.new_ratio
  )
}

/** Read only the optional projection. No client-side reconstruction from prices or model dates. */
export function projectMarketActivity(
  data?: PricingData
): MarketActivityByGroup | undefined {
  if (!data?.group_market_activity || !data.usable_group) return undefined
  const offered = new Set(
    data.data?.flatMap((model) => model.enable_groups ?? [])
  )
  const entries: [string, GroupMarketActivity][] = []
  for (const [group, activity] of Object.entries(data.group_market_activity)) {
    if (
      !activity ||
      group === 'all' ||
      group === 'auto' ||
      !Object.hasOwn(data.usable_group, group) ||
      (!offered.has(group) && !offered.has('all'))
    ) {
      continue
    }
    const item: GroupMarketActivity = {}
    if (validMarketTime(activity.first_configured_at)) {
      item.first_configured_at = activity.first_configured_at
      if (activity.is_new === true) item.is_new = true
    }
    const changes = activity.rate_changes
    if (
      data.group_rate_source?.[group] === 'group' &&
      Array.isArray(changes) &&
      validMarketRateChange(changes[0]) &&
      changes[0].new_ratio === data.group_ratio?.[group]
    ) {
      item.rate_changes = changes.slice(0, 10).filter(validMarketRateChange)
      if (activity.recent_rate_change === true) item.recent_rate_change = true
    }
    if (item.first_configured_at || item.rate_changes) {
      entries.push([group, item])
    }
  }
  return entries.length ? Object.fromEntries(entries) : undefined
}

// Match the published numbers' shortest decimal representation. Subtracting
// binary floats can turn 0.3 - 0.2 into 0.09999999999999998 or erase tiny changes.
const decimal = new Intl.NumberFormat('en-US', {
  useGrouping: false,
  maximumSignificantDigits: 21,
})
export function marketRateDifference(change: MarketRateChange): string {
  const [oldWhole, oldFraction = ''] = decimal
    .format(change.old_ratio)
    .split('.')
  const [newWhole, newFraction = ''] = decimal
    .format(change.new_ratio)
    .split('.')
  const scale = Math.max(oldFraction.length, newFraction.length)
  const delta =
    BigInt(newWhole + newFraction.padEnd(scale, '0')) -
    BigInt(oldWhole + oldFraction.padEnd(scale, '0'))
  const digits = (delta < 0n ? -delta : delta)
    .toString()
    .padStart(scale + 1, '0')
  const magnitude = scale
    ? `${digits.slice(0, -scale)}.${digits.slice(-scale)}`.replace(/\.?0+$/, '')
    : digits
  return (delta < 0n ? '−' : '+') + magnitude
}
