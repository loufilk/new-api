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

import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import {
  Pagination,
  PaginationContent,
  PaginationItem,
  PaginationNext,
  PaginationPrevious,
} from '@/components/ui/pagination'
import { Skeleton } from '@/components/ui/skeleton'
import { toIntlLocale } from '@/i18n/languages'
import { formatQuota } from '@/lib/format'
import { cn } from '@/lib/utils'

import { REFERRAL_RECORDS_PAGE_SIZE } from '../constants'
import { formatReferralRate } from '../lib'
import type { ReferralCommissionItem } from '../types'

interface ReferralCommissionRecordsProps {
  records: ReferralCommissionItem[]
  total: number
  loading?: boolean
  error?: unknown
  onRetry?: () => void
  page: number
  onPageChange: (page: number) => void
}

function formatTime(timestamp: number, locale?: string): string {
  if (!timestamp) return '—'
  return new Date(timestamp * 1000).toLocaleString(locale)
}

export function ReferralCommissionRecords(
  props: ReferralCommissionRecordsProps
) {
  const { t, i18n } = useTranslation()
  const totalPages = Math.max(
    1,
    Math.ceil(props.total / REFERRAL_RECORDS_PAGE_SIZE)
  )
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)

  const columns: StaticDataTableColumn<ReferralCommissionItem>[] = [
    {
      id: 'time',
      header: t('Time'),
      cell: (row) => formatTime(row.created_at, locale),
      className: 'w-40',
    },
    {
      id: 'friend',
      header: t('Friend'),
      cell: (row) => row.source_name,
    },
    {
      id: 'paid',
      header: t('Recharge Amount'),
      className: 'text-right',
      cell: (row) => formatQuota(row.paid_quota),
    },
    {
      id: 'reward',
      header: t('Reward'),
      className: 'text-right',
      cell: (row) => formatQuota(row.reward_quota),
    },
    {
      id: 'rate',
      header: t('Rate'),
      className: 'text-right',
      cell: (row) => formatReferralRate(row.rate),
    },
  ]

  if (props.loading) {
    return (
      <div className='space-y-2'>
        {[0, 1, 2].map((key) => (
          <Skeleton key={key} className='h-9 w-full' />
        ))}
      </div>
    )
  }

  if (props.error) {
    return <ErrorState className='min-h-24 p-3' onRetry={props.onRetry} />
  }

  const pager = (
    <div className='flex items-center justify-between gap-2'>
      <span className='text-muted-foreground text-xs'>
        {t('Total:')} {props.total}
      </span>
      <RecordsPager
        page={props.page}
        totalPages={totalPages}
        onPageChange={props.onPageChange}
      />
    </div>
  )

  if (props.records.length === 0) {
    return (
      <div className='space-y-2'>
        <EmptyState
          className='min-h-24 p-3'
          title={t(
            'No commission records yet. Invite friends to recharge and they will appear here.'
          )}
        />
        {pager}
      </div>
    )
  }

  return (
    <div className='space-y-2'>
      <div
        className='max-h-24 overflow-auto 2xl:max-h-36'
        role='region'
        aria-label={t('Commission Records')}
        tabIndex={0}
      >
        <StaticDataTable
          columns={columns.map((column) => ({
            ...column,
            className: cn('h-8 text-xs', column.className),
            cellClassName: 'py-2 text-xs',
          }))}
          data={props.records}
          getRowKey={(row) => row.id}
        />
      </div>
      {pager}
    </div>
  )
}

function RecordsPager(props: {
  page: number
  totalPages: number
  onPageChange: (page: number) => void
}) {
  const { t } = useTranslation()
  const canGoPrevious = props.page > 1
  const canGoNext = props.page < props.totalPages
  const disabledLinkClass = 'pointer-events-none opacity-50'

  return (
    <Pagination aria-label={t('Commission Records')} className='mx-0 w-auto'>
      <PaginationContent>
        <PaginationItem>
          <PaginationPrevious
            text={t('Previous')}
            href='#'
            aria-disabled={!canGoPrevious}
            tabIndex={canGoPrevious ? undefined : -1}
            className={cn(!canGoPrevious && disabledLinkClass)}
            onClick={(event) => {
              event.preventDefault()
              if (canGoPrevious) {
                props.onPageChange(props.page - 1)
              }
            }}
          />
        </PaginationItem>
        <PaginationItem>
          <span
            className='text-muted-foreground px-1 text-xs tabular-nums'
            aria-live='polite'
          >
            {props.page} / {props.totalPages}
          </span>
        </PaginationItem>
        <PaginationItem>
          <PaginationNext
            text={t('Next')}
            href='#'
            aria-disabled={!canGoNext}
            tabIndex={canGoNext ? undefined : -1}
            className={cn(!canGoNext && disabledLinkClass)}
            onClick={(event) => {
              event.preventDefault()
              if (canGoNext) {
                props.onPageChange(props.page + 1)
              }
            }}
          />
        </PaginationItem>
      </PaginationContent>
    </Pagination>
  )
}
