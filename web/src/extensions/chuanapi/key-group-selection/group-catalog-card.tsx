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
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { CommandItem } from '@/components/ui/command'
import type { ApiKeyGroupOption } from '@/features/keys/components/api-key-group-combobox'
import { ApiKeyGroupHealth } from '@/features/keys/components/api-key-group-health'
import { GroupRatioBadge } from '@/features/keys/components/auto-group-visuals'
import { cn } from '@/lib/utils'

type GroupCatalogCardProps = {
  option: ApiKeyGroupOption
  selected: boolean
  disabled?: boolean
  onSelect: () => void
}

export function GroupCatalogCard(props: GroupCatalogCardProps) {
  const { t } = useTranslation()
  let availability = t('Availability unknown')
  if (props.option.health?.available === true) availability = t('Available')
  if (props.option.health?.available === false) availability = t('Unavailable')

  return (
    <CommandItem
      value={props.option.value}
      aria-checked={props.selected}
      data-checked={props.selected}
      disabled={props.disabled}
      onSelect={props.onSelect}
      className={cn(
        'min-w-0 items-start rounded-lg border p-3',
        props.selected && 'border-primary/50 bg-primary/5'
      )}
    >
      <span className='block min-w-0 flex-1'>
        <span className='block font-medium [overflow-wrap:anywhere]'>
          {props.option.label}
        </span>
        {props.option.desc && (
          <span className='text-muted-foreground mt-1 block text-xs [overflow-wrap:anywhere]'>
            {props.option.desc}
          </span>
        )}
        <span className='mt-3 flex flex-wrap items-center justify-between gap-2'>
          <span className='flex items-center gap-2'>
            <span className='text-muted-foreground text-xs'>
              {t('Effective multiplier')}
            </span>
            {props.option.ratio === undefined ? (
              <span className='text-muted-foreground text-xs'>
                {t('Unknown')}
              </span>
            ) : (
              <GroupRatioBadge ratio={props.option.ratio} />
            )}
          </span>
          <Badge variant='outline'>{availability}</Badge>
        </span>
        <ApiKeyGroupHealth detail={props.option.health} />
      </span>
    </CommandItem>
  )
}
