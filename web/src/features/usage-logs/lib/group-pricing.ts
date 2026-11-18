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
import type { LogOtherData } from '../types'

type LogGroupPricing = {
  ratio: number | null
  source: 'user' | 'account_group' | 'global'
}

/** Resolve the recorded billing snapshot without applying current prices. */
export function resolveLogGroupPricing(
  other: LogOtherData | null | undefined
): LogGroupPricing {
  const personalRatio = other?.user_group_price_override
  if (
    typeof personalRatio === 'number' &&
    Number.isFinite(personalRatio) &&
    personalRatio >= 0
  ) {
    return { ratio: personalRatio, source: 'user' }
  }

  // Historical logs use -1 to indicate that no account-class rule applied.
  const accountRatio = other?.user_group_ratio
  if (
    typeof accountRatio === 'number' &&
    Number.isFinite(accountRatio) &&
    accountRatio >= 0
  ) {
    return { ratio: accountRatio, source: 'account_group' }
  }

  const groupRatio = other?.group_ratio
  if (
    typeof groupRatio === 'number' &&
    Number.isFinite(groupRatio) &&
    groupRatio >= 0
  ) {
    return { ratio: groupRatio, source: 'global' }
  }
  return { ratio: null, source: 'global' }
}
