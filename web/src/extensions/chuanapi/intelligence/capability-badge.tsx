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
  Check,
  Clock,
  LoaderCircle,
  TrendingDown,
  TriangleAlert,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { cn } from '@/lib/utils'

import { modelCapability } from './timeline'
import type { TestRun } from './types'

export function ModelCapabilityBadge(props: {
  run?: TestRun
  className?: string
}) {
  const { t } = useTranslation()
  const capability = props.run ? modelCapability(props.run) : null
  let label = t('Awaiting first result')
  let variant: StatusVariant = 'green'
  let icon = Clock
  if (capability === 'full') {
    label = t('Full capability')
    icon = Check
  } else if (capability === 'reduced') {
    label = t('Reduced capability')
    variant = 'red'
    icon = TrendingDown
  } else if (props.run?.status === 'running') {
    label = t('intelligence.status.running')
    icon = LoaderCircle
  } else if (props.run) {
    label = t('Test error')
    variant = 'warning'
    icon = TriangleAlert
    if (
      props.run.status === 'cancelled' ||
      props.run.status === 'intercepted'
    ) {
      label = t(`intelligence.status.${props.run.status}`)
    }
  }
  return (
    <StatusBadge
      copyable={false}
      label={label}
      variant={variant}
      icon={icon}
      className={cn(
        'h-auto min-h-5 rounded-md px-1.5 py-0.5 text-[11px]',
        props.className
      )}
    >
      <span className='min-w-0 whitespace-normal'>{label}</span>
    </StatusBadge>
  )
}
