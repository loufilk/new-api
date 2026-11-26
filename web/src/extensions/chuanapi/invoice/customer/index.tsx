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
import { useIsFetching, useQuery, useQueryClient } from '@tanstack/react-query'
import { Wallet, Clock3, FileCheck2, RotateCw } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { IconBadge } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import { getServerErrorStatus } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import { invoiceAPI, invoiceKey, privateRead } from './api'
import { ApplicationForm } from './application-form'
import { useInvoiceText } from './copy'
import { ApplicationDetail } from './detail'
import { InvoiceHistory } from './history'
import { EligibleOrders, type OrderSelection } from './orders'

function CustomerInvoiceCenter(props: { owner: number }) {
  const client = useQueryClient()
  const { text, money } = useInvoiceText()
  const [selection, setSelection] = useState<OrderSelection>(() => new Map())
  const [detailId, setDetailId] = useState<number | null>(null)
  const [historyReset, setHistoryReset] = useState(0)
  const [busy, setBusy] = useState(false)
  const config = useQuery({
    ...privateRead,
    queryKey: [...invoiceKey(props.owner), 'config'],
    queryFn: ({ signal }) => invoiceAPI.config(signal),
  })
  const totals = useQuery({
    ...privateRead,
    queryKey: [...invoiceKey(props.owner), 'summary'],
    queryFn: ({ signal }) => invoiceAPI.totals(signal),
  })
  // A background read failure is not a decision to revoke invoicing. Keep
  // the draft mounted, but only enable submission after a current policy read.
  const denied = [401, 403].includes(getServerErrorStatus(config.error) ?? 0)
  const policy = denied ? undefined : config.data
  const policyReady = config.isSuccess && !config.isFetching
  const accepting =
    policy?.enabled &&
    policy.categories.length > 0 &&
    (policy.ordinary.enabled || policy.special.enabled)
  const fetching = useIsFetching({ queryKey: invoiceKey(props.owner) }) > 0
  const stats = [
    {
      key: 'available',
      label: 'Available to invoice',
      icon: Wallet,
      tone: 'info',
      count: '{{n}} orders',
    },
    {
      key: 'pending',
      label: 'Pending invoice amount',
      icon: Clock3,
      tone: 'warning',
      count: '{{n}} applications',
    },
    {
      key: 'issued',
      label: 'Issued invoice amount',
      icon: FileCheck2,
      tone: 'success',
      count: '{{n}} applications',
    },
  ] as const
  const refresh = () => {
    setSelection(new Map())
    void client.invalidateQueries({ queryKey: invoiceKey(props.owner) })
  }
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {text('Invoice center')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button
          size='sm'
          variant='outline'
          disabled={busy || fetching}
          onClick={() => {
            if (selection.size) {
              toast.info(
                text(
                  'Order selection cleared. Review the refreshed list before submitting.'
                )
              )
            }
            refresh()
          }}
        >
          <RotateCw
            aria-hidden='true'
            className={fetching ? 'animate-spin' : undefined}
          />
          {fetching ? text('Refreshing…') : text('Refresh')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='space-y-4'>
          <div className='grid gap-3 sm:grid-cols-3'>
            {stats.map((stat) => (
              <Card key={stat.key} className='min-w-0'>
                <CardContent className='flex items-start justify-between gap-3'>
                  <div className='min-w-0'>
                    <p className='text-muted-foreground text-sm'>
                      {text(stat.label)}
                    </p>
                    {totals.isPending ? (
                      <Skeleton className='mt-2 h-7 w-28' />
                    ) : (
                      <p className='mt-2 text-xl font-semibold tracking-tight break-all tabular-nums'>
                        {totals.data
                          ? money(totals.data[`${stat.key}_minor`])
                          : '—'}
                      </p>
                    )}
                    {totals.data && (
                      <p className='text-muted-foreground mt-1 text-xs'>
                        {text(stat.count, {
                          n: totals.data[`${stat.key}_count`],
                        })}
                      </p>
                    )}
                  </div>
                  <IconBadge tone={stat.tone} size='stat'>
                    <stat.icon aria-hidden='true' />
                  </IconBadge>
                </CardContent>
              </Card>
            ))}
          </div>
          {totals.isError && (
            <Button
              variant='outline'
              size='sm'
              onClick={() => void totals.refetch()}
            >
              {text('Retry')}
            </Button>
          )}
          {config.isPending && <LoadingState className='min-h-20' />}
          {config.isError && (
            <ErrorState
              className='min-h-28'
              title={text('Unable to load invoice information')}
              onRetry={() => void config.refetch()}
            />
          )}
          {config.isSuccess && !accepting && (
            <p className='bg-muted rounded-lg p-3 text-sm'>
              {text(
                'New applications are currently closed. You can still view and manage previous applications below.'
              )}
            </p>
          )}
          <div
            className={
              accepting
                ? 'grid items-start gap-4 lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]'
                : 'grid gap-4'
            }
          >
            <div className='contents lg:block lg:min-w-0 lg:space-y-4'>
              {accepting && (
                <EligibleOrders
                  key={`orders-${historyReset}`}
                  owner={props.owner}
                  selection={selection}
                  onChange={setSelection}
                  locked={busy || !policyReady}
                />
              )}
              <div className='order-last min-w-0 lg:order-none'>
                <InvoiceHistory
                  key={historyReset}
                  owner={props.owner}
                  onDetail={setDetailId}
                />
              </div>
            </div>
            {accepting && (
              <ApplicationForm
                policy={policy}
                policyReady={policyReady}
                selection={selection}
                onBusy={setBusy}
                onConflict={refresh}
                onSuccess={(paymentId) => {
                  if (paymentId) setDetailId(paymentId)
                  refresh()
                  setHistoryReset((previous) => previous + 1)
                }}
              />
            )}
          </div>
        </div>
        {detailId !== null && (
          <ApplicationDetail
            key={detailId}
            owner={props.owner}
            id={detailId}
            paymentMethods={
              policyReady ? (policy?.fee_payment_methods ?? []) : []
            }
            onClose={() => setDetailId(null)}
          />
        )}
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

export default function InvoicePage() {
  const owner = useAuthStore((s) => s.auth.user?.id)
  if (!owner) return <LoadingState />
  return <CustomerInvoiceCenter key={owner} owner={owner} />
}
