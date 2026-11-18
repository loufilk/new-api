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
import { FileText } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { toIntlLocale } from '@/i18n/languages'

import type { InvoiceApplication } from '../types'
import { ApplicationStatus, InvoiceMoney } from './application-status'

export function InvoiceHistory(props: {
  admin: boolean
  items: InvoiceApplication[]
  total: number
  loading: boolean
  error: boolean
  status: string
  page: number
  onStatus: (value: string) => void
  onPage: (value: number) => void
  onSelect: (id: number) => void
  onRetry: () => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const pages = Math.max(1, Math.ceil(props.total / 20))
  const statuses = [
    { value: '', label: t('All statuses') },
    { value: 'awaiting_fee', label: t('Awaiting tax fee') },
    {
      value: props.admin ? 'submitted' : 'reviewing',
      label: t('Under review'),
    },
    ...(props.admin ? [{ value: 'approved', label: t('Approved') }] : []),
    { value: 'issued', label: t('Invoice issued') },
    { value: 'rejected', label: t('Rejected') },
    { value: 'cancelled', label: t('Cancelled') },
  ]
  return (
    <Card className='min-w-0 rounded-3xl'>
      <CardHeader className='flex flex-row flex-wrap items-center justify-between gap-2'>
        <CardTitle>
          {props.admin ? t('Application queue') : t('My applications')}
        </CardTitle>
        <Select
          value={props.status}
          onValueChange={(value) => {
            if (typeof value === 'string') props.onStatus(value)
          }}
        >
          <SelectTrigger
            className='min-w-36 rounded-full'
            aria-label={t('Filter by status')}
          >
            <SelectValue>
              {statuses.find((item) => item.value === props.status)?.label}
            </SelectValue>
          </SelectTrigger>
          <SelectContent>
            {statuses.map((item) => (
              <SelectItem value={item.value} key={item.value}>
                {item.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </CardHeader>
      <CardContent className='space-y-3'>
        {props.loading && <LoadingState />}
        {props.error && <ErrorState onRetry={props.onRetry} />}
        {!props.loading && !props.error && !props.items.length && (
          <EmptyState
            icon={FileText}
            className='min-h-52'
            title={t('No invoice applications')}
            description={t(
              'Your submitted invoice applications will appear here.'
            )}
          />
        )}
        <div
          className={props.admin ? 'grid gap-3 md:grid-cols-2' : 'grid gap-3'}
        >
          {props.items.map((row) => (
            <div key={row.id} className='rounded-2xl border p-4'>
              <div className='flex items-start justify-between gap-3'>
                <div className='min-w-0'>
                  <h3 className='truncate font-medium'>{row.title}</h3>
                  <p className='text-muted-foreground mt-1 text-xs'>
                    #{row.id}
                    {props.admin &&
                      ` · ${t('User #{{id}}', { id: row.user_id })}`}
                  </p>
                </div>
                <ApplicationStatus
                  status={
                    !props.admin &&
                    ['submitted', 'approved'].includes(row.status)
                      ? 'reviewing'
                      : row.status
                  }
                />
              </div>
              <div className='mt-4 flex flex-wrap items-end justify-between gap-3'>
                <div>
                  <p className='text-lg font-semibold'>
                    <InvoiceMoney
                      minor={row.amount_minor}
                      currency={row.currency}
                      exponent={row.currency_exponent}
                    />
                  </p>
                  <p className='text-muted-foreground mt-1 text-xs'>
                    {new Date(row.created_at * 1000).toLocaleString(locale)}
                  </p>
                </div>
                <Button
                  variant='outline'
                  className='rounded-full'
                  onClick={() => props.onSelect(row.id)}
                >
                  {t('View details')}
                </Button>
              </div>
            </div>
          ))}
        </div>
        {pages > 1 && (
          <div className='flex flex-wrap items-center justify-end gap-3 pt-2'>
            <Button
              variant='outline'
              disabled={props.page === 1}
              onClick={() => props.onPage(props.page - 1)}
            >
              {t('Previous')}
            </Button>
            <span className='text-muted-foreground text-xs'>
              {t('Page {{page}} of {{total}}', {
                page: props.page,
                total: pages,
              })}
            </span>
            <Button
              variant='outline'
              disabled={props.page >= pages}
              onClick={() => props.onPage(props.page + 1)}
            >
              {t('Next')}
            </Button>
          </div>
        )}
      </CardContent>
    </Card>
  )
}
