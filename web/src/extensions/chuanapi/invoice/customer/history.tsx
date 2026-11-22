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
import { FileText } from 'lucide-react'
import { useState } from 'react'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardAction,
  CardHeader,
  CardTitle,
  CardContent,
} from '@/components/ui/card'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { formatTimestamp } from '@/lib/format'

import { invoiceCategorySnapshotLabel, type InvoiceStatus } from '../contracts'
import { invoiceAPI, invoiceKey, privateRead } from './api'
import {
  useInvoiceText,
  statusLabels,
  invoiceTypeLabels,
  applicationStatusLabel,
} from './copy'
import { InvoicePager } from './pager'

const tones = {
  awaiting_payment: 'warning',
  pending: 'warning',
  issued: 'success',
  rejected: 'danger',
  cancelled: 'neutral',
} as const

export function InvoiceHistory(props: {
  owner: number
  onDetail: (id: number) => void
}) {
  const { text, money } = useInvoiceText()
  const [status, setStatus] = useState<InvoiceStatus | ''>('')
  const [page, setPage] = useState(1)
  const result = useQuery({
    ...privateRead,
    queryKey: [...invoiceKey(props.owner), 'history', status, page],
    queryFn: ({ signal }) => invoiceAPI.history(page, status, signal),
  })
  return (
    <Card className='min-w-0'>
      <CardHeader>
        <CardTitle>{text('My applications')}</CardTitle>
        <CardAction>
          <NativeSelect
            size='sm'
            aria-label={text('Application status')}
            value={status}
            onChange={(e) => {
              setStatus(e.target.value as InvoiceStatus | '')
              setPage(1)
            }}
          >
            <NativeSelectOption value=''>
              {text('All statuses')}
            </NativeSelectOption>
            {Object.entries(statusLabels).map(([value, label]) => (
              <NativeSelectOption key={value} value={value}>
                {text(value === 'pending' ? 'In progress' : label)}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </CardAction>
      </CardHeader>
      <CardContent>
        {result.isPending && <LoadingState className='min-h-24' size='sm' />}
        {result.isError && (
          <ErrorState
            className='min-h-24'
            title={text('Unable to load invoice information')}
            onRetry={() => void result.refetch()}
          />
        )}
        {result.isSuccess && (
          <>
            {!result.data.items?.length && (
              <EmptyState
                className='min-h-28'
                icon={FileText}
                title={text('No applications found')}
              />
            )}
            <ul className='divide-y'>
              {(result.data.items || []).map((entry) => (
                <li
                  key={entry.id}
                  className='flex flex-wrap items-center justify-between gap-x-3 gap-y-2 py-3 first:pt-0'
                >
                  <div className='min-w-0 space-y-1'>
                    <div className='flex flex-wrap items-center gap-2'>
                      <span className='font-medium'>
                        {text('Application')} #{entry.id}
                      </span>
                      <StatusBadge
                        label={text(applicationStatusLabel(entry))}
                        variant={tones[entry.status]}
                        copyable={false}
                      />
                    </div>
                    <p className='text-muted-foreground text-xs break-words'>
                      {formatTimestamp(entry.created_at)} ·{' '}
                      {text(invoiceTypeLabels[entry.invoice_type])} ·{' '}
                      {invoiceCategorySnapshotLabel(
                        entry.category,
                        entry.category_name,
                        (key) => text(key)
                      )}
                    </p>
                  </div>
                  <div className='flex flex-wrap items-center gap-3'>
                    <span className='font-medium tabular-nums'>
                      {money(entry.amount_minor)}
                    </span>
                    <Button
                      size='sm'
                      variant='outline'
                      onClick={() => props.onDetail(entry.id)}
                      aria-label={`${text('Details')} #${entry.id}`}
                    >
                      {text('Details')}
                    </Button>
                  </div>
                </li>
              ))}
            </ul>
            <InvoicePager
              page={page}
              total={result.data.total}
              label={text('My applications')}
              onChange={setPage}
            />
          </>
        )}
      </CardContent>
    </Card>
  )
}
