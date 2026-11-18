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
  ArrowUpRight,
  CheckCircle2,
  CircleHelp,
  TriangleAlert,
  Unplug,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { ApiKeyGroupHealth } from '@/features/keys/components/api-key-group-health'
import { GroupRatioBadge } from '@/features/keys/components/auto-group-visuals'
import type { TokenGroupDetail } from '@/features/keys/types'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { groupHealth } from './health'

const healthPresentation = {
  healthy: { label: 'Healthy', variant: 'success', icon: CheckCircle2 },
  degraded: {
    label: 'Needs attention',
    variant: 'warning',
    icon: TriangleAlert,
  },
  unknown: { label: 'No samples', variant: 'neutral', icon: CircleHelp },
  unavailable: { label: 'Unavailable', variant: 'danger', icon: Unplug },
} as const

export function GroupHealthCard(props: {
  group: TokenGroupDetail
  onDetails: () => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const state = healthPresentation[groupHealth(props.group)]
  return (
    <article className='bg-card flex min-w-0 flex-col gap-4 rounded-2xl border p-5 shadow-xs transition-shadow hover:shadow-sm'>
      <div className='flex items-start justify-between gap-3'>
        <h2 className='min-w-0 text-base font-semibold [overflow-wrap:anywhere]'>
          {props.group.name}
        </h2>
        <StatusBadge
          label={t(state.label)}
          variant={state.variant}
          icon={state.icon}
          copyable={false}
          className='shrink-0'
        />
      </div>
      <p className='text-muted-foreground min-h-10 text-sm leading-relaxed [overflow-wrap:anywhere]'>
        {props.group.description || t('No group description provided')}
      </p>
      <div className='bg-muted/35 flex items-center justify-between gap-3 rounded-xl px-3 py-2.5'>
        <span className='text-muted-foreground text-xs'>
          {t('Your effective multiplier')}
        </span>
        <GroupRatioBadge ratio={props.group.ratio} />
      </div>
      <ApiKeyGroupHealth detail={props.group} />
      <div className='mt-auto flex items-center justify-between gap-3 border-t pt-3'>
        <span className='text-muted-foreground text-xs'>
          {props.group.request_count == null
            ? t('Sample count unavailable')
            : t('{{samples}} recorded attempts', {
                samples: formatNumber(props.group.request_count, locale),
              })}
        </span>
        <Button
          variant='ghost'
          size='sm'
          onClick={props.onDetails}
          aria-label={t('View {{group}} details', { group: props.group.name })}
        >
          {t('Details')}
          <ArrowUpRight className='size-3.5' aria-hidden />
        </Button>
      </div>
    </article>
  )
}
