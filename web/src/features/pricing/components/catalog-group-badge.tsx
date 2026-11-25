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
import { GroupBadge } from '@/components/group-badge'

import { marketGroupLabel } from '../lib/market-groups'
import type { MarketGroupCatalog } from '../types'
import { MarketGroupIcon } from './market-group-icon'

export function CatalogGroupBadge(props: {
  group: string
  catalog?: MarketGroupCatalog
}) {
  return (
    <span className='inline-flex max-w-full min-w-0 items-center gap-1'>
      <MarketGroupIcon meta={props.catalog?.[props.group]} size={14} />
      <GroupBadge
        group={props.group}
        label={marketGroupLabel(props.group, props.catalog)}
        size='sm'
      />
    </span>
  )
}
