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
import { useQuery } from '@tanstack/react-query'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Empty, EmptyDescription, EmptyTitle } from '@/components/ui/empty'
import { NativeSelect } from '@/components/ui/native-select'
import {
  Pagination,
  PaginationContent,
  PaginationItem,
  PaginationLink,
  PaginationNext,
  PaginationPrevious,
} from '@/components/ui/pagination'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { formatQuota } from '@/lib/format'
import { requireServerSuccess } from '@/lib/server-error-message'

import { getWalletLedger } from '../api'
import { ledgerReasonLabelKey } from '../lib/ledger'
import type { WalletLedgerRow } from '../types'

const PAGE_SIZE = 10

const ACCOUNT_OPTIONS = [
  { value: '', labelKey: 'All Accounts' },
  { value: 'paid', labelKey: 'Paid Balance' },
  { value: 'bonus', labelKey: 'Bonus Balance' },
  { value: 'reward', labelKey: 'Reward Balance' },
]

function accountTone(account: string): string {
  switch (account) {
    case 'paid':
      return 'bg-blue-500/10 text-blue-600 dark:text-blue-400'
    case 'bonus':
      return 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400'
    case 'reward':
      return 'bg-amber-500/10 text-amber-600 dark:text-amber-400'
    default:
      return 'bg-muted text-muted-foreground'
  }
}

export function LedgerTable() {
  const { t } = useTranslation()
  const [page, setPage] = useState(1)
  const [account, setAccount] = useState('')
  const [reason, setReason] = useState('')

  const ledgerQuery = useQuery({
    queryKey: ['wallet', 'ledger', page, account, reason],
    queryFn: async () => {
      const response = requireServerSuccess(
        await getWalletLedger({
          p: page,
          page_size: PAGE_SIZE,
          account: account || undefined,
          reason: reason || undefined,
        })
      )
      if (!response.data) throw new Error('Wallet ledger is unavailable')
      return response.data
    },
  })

  const rows: WalletLedgerRow[] = ledgerQuery.data?.items ?? []
  const total = ledgerQuery.data?.total ?? 0

  const totalPages = useMemo(
    () => Math.max(1, Math.ceil(total / PAGE_SIZE)),
    [total]
  )

  return (
    <Card data-card-hover='false' className='bg-muted/20 py-0'>
      <CardHeader className='flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between'>
        <CardTitle>{t('Balance Ledger')}</CardTitle>
        <div className='flex flex-wrap items-center gap-2'>
          <NativeSelect
            aria-label={t('Filter by account')}
            value={account}
            onChange={(event) => {
              setAccount(event.currentTarget.value)
              setPage(1)
            }}
          >
            {ACCOUNT_OPTIONS.map((option) => (
              <option key={option.value} value={option.value}>
                {t(option.labelKey)}
              </option>
            ))}
          </NativeSelect>
          <input
            aria-label={t('Filter by reason')}
            type='text'
            value={reason}
            onChange={(event) => {
              setReason(event.currentTarget.value)
              setPage(1)
            }}
            placeholder={t('Reason')}
            className='border-muted bg-background/70 h-9 w-32 rounded-md border px-2.5 text-xs'
          />
        </div>
      </CardHeader>
      <CardContent className='p-0 sm:p-0'>{renderBody()}</CardContent>
    </Card>
  )

  function renderBody() {
    if (ledgerQuery.isLoading) {
      return (
        <div className='space-y-2 p-4'>
          {[0, 1, 2, 3].map((key) => (
            <Skeleton key={key} className='h-10 w-full' />
          ))}
        </div>
      )
    }
    if (ledgerQuery.isError) {
      return (
        <ErrorState
          className='min-h-[220px]'
          onRetry={() => void ledgerQuery.refetch()}
        />
      )
    }
    if (rows.length === 0) {
      return (
        <Empty className='p-8'>
          <EmptyTitle>{t('No ledger records yet')}</EmptyTitle>
          <EmptyDescription>
            {t('Fund movements will appear here')}
          </EmptyDescription>
        </Empty>
      )
    }
    return (
      <LedgerContent
        rows={rows}
        total={total}
        page={page}
        totalPages={totalPages}
        onPageChange={setPage}
      />
    )
  }
}

function accountLabel(account: string, t: (key: string) => string): string {
  if (account === 'paid') return t('Paid Balance')
  if (account === 'bonus') return t('Bonus Balance')
  if (account === 'reward') return t('Reward Balance')
  return t('Unknown Account')
}

function deltaTone(delta: number): string {
  if (delta > 0) return 'text-emerald-600 dark:text-emerald-400'
  if (delta < 0) return 'text-rose-600 dark:text-rose-400'
  return ''
}

function formatTime(ts: number): string {
  if (!ts) return '—'
  return new Date(ts * 1000).toLocaleString()
}

function LedgerContent(props: {
  rows: WalletLedgerRow[]
  total: number
  page: number
  totalPages: number
  onPageChange: (page: number) => void
}) {
  const { t } = useTranslation()
  const rows = props.rows
  return (
    <>
      <div className='overflow-x-auto'>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Time')}</TableHead>
              <TableHead>{t('Account')}</TableHead>
              <TableHead className='text-right'>{t('Delta')}</TableHead>
              <TableHead className='text-right'>{t('Balance')}</TableHead>
              <TableHead>{t('Reason')}</TableHead>
              <TableHead>{t('Reference')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((row) => (
              <TableRow key={row.id}>
                <TableCell className='text-muted-foreground text-xs whitespace-nowrap'>
                  {formatTime(row.created_at)}
                </TableCell>
                <TableCell>
                  <span
                    className={`inline-flex items-center rounded px-1.5 py-0.5 text-[11px] font-medium ${accountTone(row.account)}`}
                  >
                    {accountLabel(row.account, t)}
                  </span>
                </TableCell>
                <TableCell
                  className={`text-right font-mono text-xs font-semibold tabular-nums ${deltaTone(row.delta)}`}
                >
                  {row.delta > 0 ? '+' : ''}
                  {formatQuota(row.delta)}
                </TableCell>
                <TableCell className='text-right font-mono text-xs tabular-nums'>
                  {formatQuota(row.after)}
                </TableCell>
                <TableCell className='text-xs'>
                  {t(ledgerReasonLabelKey(row.reason))}
                </TableCell>
                <TableCell className='text-muted-foreground max-w-40 truncate font-mono text-xs'>
                  {row.reference || row.request_id || '—'}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
      <div className='flex items-center justify-between border-t p-3'>
        <div className='text-muted-foreground text-xs'>
          {t('Total')}: {props.total}
        </div>
        <Pagination>
          <PaginationContent>
            <PaginationItem>
              <PaginationPrevious
                text={t('Previous')}
                href='#'
                onClick={(event) => {
                  event.preventDefault()
                  if (props.page > 1) {
                    props.onPageChange(props.page - 1)
                  }
                }}
                aria-disabled={props.page <= 1}
              />
            </PaginationItem>
            {Array.from({ length: props.totalPages }, (_, index) => index + 1)
              .filter(
                (p) =>
                  p === 1 ||
                  p === props.totalPages ||
                  Math.abs(p - props.page) <= 1
              )
              .map((p, index, list) => (
                <PaginationItem
                  key={p}
                  className={
                    index > 0 && list[index - 1] !== p - 1 ? 'ms-1' : ''
                  }
                >
                  <PaginationLink
                    href='#'
                    isActive={p === props.page}
                    onClick={(event) => {
                      event.preventDefault()
                      props.onPageChange(p)
                    }}
                  >
                    {p}
                  </PaginationLink>
                </PaginationItem>
              ))}
            <PaginationItem>
              <PaginationNext
                text={t('Next')}
                href='#'
                onClick={(event) => {
                  event.preventDefault()
                  if (props.page < props.totalPages) {
                    props.onPageChange(props.page + 1)
                  }
                }}
                aria-disabled={props.page >= props.totalPages}
              />
            </PaginationItem>
          </PaginationContent>
        </Pagination>
      </div>
    </>
  )
}
