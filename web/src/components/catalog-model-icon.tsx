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
import {
  catalogModelIconID,
  type CatalogIconSource,
} from '@/lib/catalog-icon-registry'
import { getLobeIcon } from '@/lib/lobe-icon'

// Shared by public cards, admin rows and the live preview. Do not guess the
// upstream provider from a model name or endpoint; use only catalog evidence.
export function CatalogModelIcon(
  props: CatalogIconSource & {
    name?: string | null
    size: number
  }
) {
  const key = catalogModelIconID(props)
  return (
    <span
      aria-hidden='true'
      className='inline-flex shrink-0 items-center justify-center'
    >
      {key ? (
        getLobeIcon(key, props.size)
      ) : (
        <span className='text-muted-foreground font-semibold'>
          {[...(props.name?.trim() || '?')][0].toUpperCase()}
        </span>
      )}
    </span>
  )
}
