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

import { Layers3 } from 'lucide-react'

import { getLobeIcon } from '@/lib/lobe-icon'

import {
  groupPlatform,
  MARKET_PLATFORM_ICONS,
  type MarketPlatform,
} from '../lib/market-groups'
import type { MarketGroup } from '../types'

export function MarketGroupIcon(props: {
  meta?: MarketGroup
  platform?: MarketPlatform
  size?: number
}) {
  const custom = props.meta?.icon
  const platform =
    custom && Object.hasOwn(MARKET_PLATFORM_ICONS, custom)
      ? (custom as MarketPlatform)
      : (props.platform ?? groupPlatform(props.meta))
  const size = props.size ?? 15
  const key = MARKET_PLATFORM_ICONS[platform]
  return key ? getLobeIcon(key, size) : <Layers3 size={size} aria-hidden />
}
