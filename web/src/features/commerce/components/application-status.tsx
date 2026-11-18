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
import { formatMinorMoney } from '@/features/agent-platform/lib/money'
import { toIntlLocale } from '@/i18n/languages'

import type { ApplicationEvent } from '../types'

const statusLabels: Record<string, string> = {
  awaiting_fee: 'Awaiting service fee',
  submitted: 'Pending review',
  reviewing: 'Under review',
  approved: 'Approved',
  issued: 'Invoice issued',
  rejected: 'Rejected',
  cancelled: 'Cancelled',
}

export function ApplicationStatus(props: { status: string }) {
  const { t } = useTranslation()
  let variant: 'outline' | 'secondary' | 'default' | 'destructive' = 'secondary'
  if (props.status === 'approved' || props.status === 'issued') {
    variant = 'default'
  }
  if (props.status === 'rejected') variant = 'destructive'
  if (props.status === 'cancelled') variant = 'outline'
  return (
    <Badge variant={variant}>
      {t(statusLabels[props.status] ?? props.status)}
    </Badge>
  )
}

export function InvoiceMoney(props: {
  minor: string
  currency: string
  exponent: number
}) {
  const { i18n } = useTranslation()
  return (
    <span className='font-mono tabular-nums'>
      {formatMinorMoney(
        props.minor,
        props.currency,
        props.exponent,
        toIntlLocale(i18n.resolvedLanguage || i18n.language)
      )}
    </span>
  )
}

export function ApplicationTimeline(props: { events: ApplicationEvent[] }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  return (
    <ol
      className='grid gap-3 border-l pl-4'
      aria-label={t('Application history')}
    >
      {props.events.map((event) => (
        <li
          key={event.id}
          className='flex flex-wrap items-center justify-between gap-2 text-sm'
        >
          <div className='flex flex-wrap items-center gap-2'>
            <ApplicationStatus status={event.to_status} />
            {event.action === 'confirm_fee' && (
              <span className='text-muted-foreground text-xs'>
                {t('Record service fee receipt')}
              </span>
            )}
            {event.action === 'record_fee_refund' && (
              <span className='text-muted-foreground text-xs'>
                {t('Service fee refunded')}
              </span>
            )}
          </div>
          <time
            className='text-muted-foreground text-xs'
            dateTime={new Date(event.created_at * 1000).toISOString()}
          >
            {new Date(event.created_at * 1000).toLocaleString(locale)}
          </time>
          {event.note && (
            <p className='text-muted-foreground w-full break-words whitespace-pre-wrap'>
              {event.note}
            </p>
          )}
        </li>
      ))}
    </ol>
  )
}
