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

import { TruncatedCell } from '@/components/data-table'
import { Badge } from '@/components/ui/badge'

type ApiKeyGroupCellProps = {
  group: string
  label?: string
  autoGroups?: string[]
  smartRouting?: boolean
  ratio?: number | string
}

export function ApiKeyGroupCell({
  group,
  label,
  autoGroups,
  smartRouting,
  ratio,
}: ApiKeyGroupCellProps) {
  const { t, i18n } = useTranslation()
  const legacy = smartRouting || group === 'auto' || !group
  const title = smartRouting
    ? t('Legacy smart routing')
    : group === 'auto'
      ? t('Legacy automatic routing')
      : group
        ? label || group
        : t('Follow user group')
  const multiplier =
    !legacy && typeof ratio === 'number' && Number.isFinite(ratio)
      ? `${new Intl.NumberFormat(i18n.resolvedLanguage || i18n.language, { maximumFractionDigits: 8 }).format(ratio)}×`
      : null
  const detail =
    !smartRouting && group === 'auto' && autoGroups?.length
      ? `${title}: ${autoGroups.join(' → ')}`
      : [title, multiplier].filter(Boolean).join(' · ')

  return (
    <TruncatedCell
      className='w-full'
      tabIndex={0}
      tooltipContent={detail}
      tooltipClassName='break-all'
    >
      <span className='flex min-w-0 items-center gap-2'>
        <Badge variant='secondary' className='max-w-38 min-w-0'>
          <span className='truncate'>{title}</span>
        </Badge>
        {multiplier && (
          <span className='text-muted-foreground text-xs'>{multiplier}</span>
        )}
      </span>
    </TruncatedCell>
  )
}
