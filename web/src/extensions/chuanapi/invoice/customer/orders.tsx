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
import { Link } from '@tanstack/react-router'
import { ReceiptText } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'

import { StaticDataTable } from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import {
  Card,
  CardHeader,
  CardTitle,
  CardDescription,
  CardContent,
} from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { formatTimestamp } from '@/lib/format'

import type { InvoiceOrder } from '../contracts'
import { invoiceAPI, invoiceKey, ORDER_LIMIT, privateRead } from './api'
import { useInvoiceText } from './copy'
import { InvoicePager } from './pager'

export type OrderSelection = Map<number, string>

export function EligibleOrders(props: {
  owner: number
  selection: OrderSelection
  onChange: (next: OrderSelection) => void
  locked: boolean
}) {
  const { text, money } = useInvoiceText()
  const [page, setPage] = useState(1)
  const result = useQuery({
    ...privateRead,
    queryKey: [...invoiceKey(props.owner), 'orders', page],
    queryFn: ({ signal }) => invoiceAPI.orders(page, signal),
  })
  const orders = result.data?.items || []
  const selectedHere = orders.filter((order) =>
    props.selection.has(order.id)
  ).length
  function toggle(rows: InvoiceOrder[], add: boolean) {
    if (props.locked) return
    const next = new Map(props.selection)
    for (const row of rows) {
      if (add) next.set(row.id, row.amount_minor)
      else next.delete(row.id)
    }
    if (next.size > ORDER_LIMIT) {
      toast.error(
        text('Selection limit reached. Deselect orders before adding more.')
      )
      return
    }
    props.onChange(next)
  }
  return (
    <Card className='min-w-0'>
      <CardHeader>
        <CardTitle>{text('Eligible orders')}</CardTitle>
        <CardDescription>
          {text(
            'Select orders paid within the last 30 days. Up to 100 orders per application.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent>
        {result.isPending && <LoadingState size='sm' className='min-h-28' />}
        {result.isError && (
          <ErrorState
            className='min-h-28'
            title={text('Unable to load invoice information')}
            onRetry={() => void result.refetch()}
          />
        )}
        {result.isSuccess && (
          <>
            {orders.length === 0 ? (
              <EmptyState
                className='min-h-32'
                icon={ReceiptText}
                title={text('No eligible orders')}
              />
            ) : (
              <StaticDataTable
                data={orders}
                getRowKey={(order) => order.id}
                columns={[
                  {
                    id: 'select',
                    header: (
                      <Checkbox
                        checked={selectedHere === orders.length}
                        indeterminate={
                          selectedHere > 0 && selectedHere < orders.length
                        }
                        disabled={props.locked}
                        aria-label={text('Select this page')}
                        onCheckedChange={(checked) => toggle(orders, checked)}
                      />
                    ),
                    className: 'w-10',
                    cell: (order) => (
                      <Checkbox
                        checked={props.selection.has(order.id)}
                        disabled={props.locked}
                        aria-label={order.reference}
                        onCheckedChange={(checked) => toggle([order], checked)}
                      />
                    ),
                  },
                  {
                    id: 'reference',
                    header: text('Order number'),
                    cell: (order) => (
                      <div className='min-w-0'>
                        <p className='max-w-56 font-mono text-xs break-all whitespace-normal'>
                          {order.reference}
                        </p>
                        <p className='text-muted-foreground mt-1 text-xs'>
                          {formatTimestamp(order.paid_at)}
                        </p>
                      </div>
                    ),
                  },
                  {
                    id: 'amount',
                    header: text('Paid amount'),
                    className: 'text-end',
                    cellClassName: 'text-end tabular-nums',
                    cell: (order) => money(order.amount_minor),
                  },
                ]}
              />
            )}
            <InvoicePager
              page={page}
              total={result.data.total}
              label={text('Eligible orders')}
              onChange={setPage}
            />
          </>
        )}
        <p className='text-muted-foreground mt-3 text-xs'>
          {text('Orders not listed?')}{' '}
          <Link
            to='/support'
            className='text-primary underline underline-offset-2'
          >
            {text('Contact support for manual review.')}
          </Link>
        </p>
      </CardContent>
    </Card>
  )
}
