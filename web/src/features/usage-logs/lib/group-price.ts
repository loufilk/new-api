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

type GroupPriceLabel =
  | 'User Exclusive Ratio'
  | 'Account Group Ratio'
  | 'Group Ratio'

function validRate(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0
}

// Historical rows use user_group_ratio for an account-group rule. A personal
// rule or a valid explicit zero should always take precedence in a new row.
export function getLogGroupPrice(other: LogOtherData | null | undefined): {
  ratio: number | null
  label: GroupPriceLabel
} {
  if (validRate(other?.user_group_price_override)) {
    return {
      ratio: other.user_group_price_override,
      label: 'User Exclusive Ratio',
    }
  }
  if (
    validRate(other?.user_group_ratio) &&
    other?.group_ratio_source !== 'group'
  ) {
    return { ratio: other.user_group_ratio, label: 'Account Group Ratio' }
  }
  return {
    ratio: validRate(other?.group_ratio) ? other.group_ratio : null,
    label: 'Group Ratio',
  }
}
