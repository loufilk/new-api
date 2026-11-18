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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { RefreshCw } from 'lucide-react'
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { hasPermission } from '@/lib/admin-permissions'
import { useAuthStore } from '@/stores/auth-store'

import {
  createInvoice,
  getEligibleInvoiceOrders,
  getInvoice,
  listInvoices,
  reviewInvoice,
} from './api'
import { InvoiceApplicationForm } from './components/invoice-application-form'
import { InvoiceDetailDialog } from './components/invoice-detail-dialog'
import { InvoiceHistory } from './components/invoice-history'
import { InvoiceSummaryCards } from './components/invoice-summary-cards'
import { useInvoicePayment } from './hooks/use-invoice-payment'
import type {
  ApplicationReview,
  InvoiceApplication,
  InvoiceInput,
} from './types'

export function InvoicesPage(props: { admin?: boolean }) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const admin = props.admin === true
  const canReview = admin && hasPermission(user, 'commerce_invoices', 'review')
  const client = useQueryClient()
  const [status, setStatus] = useState('')
  const [page, setPage] = useState(1)
  const [selected, setSelected] = useState<number | null>(null)
  const created = useRef<{
    userId: number | undefined
    key: string
    invoice: InvoiceApplication
  } | null>(null)
  const list = useQuery({
    queryKey: ['commerce', 'invoices', user?.id, admin, status, page],
    queryFn: () => listInvoices(admin, status, page),
  })
  const eligible = useQuery({
    queryKey: ['commerce', 'invoice-orders', user?.id],
    queryFn: getEligibleInvoiceOrders,
    enabled: !admin,
  })
  const detail = useQuery({
    queryKey: ['commerce', 'invoice', user?.id, admin, selected],
    queryFn: () => getInvoice(admin, selected as number),
    enabled: selected !== null,
  })
  const payment = useInvoicePayment()
  const create = useMutation({
    mutationFn: (input: { data: InvoiceInput; key: string }) =>
      createInvoice(input.data, input.key),
  })
  const review = useMutation({
    mutationFn: (input: ApplicationReview) =>
      reviewInvoice(admin, selected as number, input),
    onSuccess: async () => {
      toast.success(t('Application updated'))
      await Promise.all([
        client.invalidateQueries({ queryKey: ['commerce'] }),
        client.invalidateQueries({ queryKey: ['wallet'] }),
      ])
    },
  })
  const history = (
    <InvoiceHistory
      admin={admin}
      items={list.data?.items ?? []}
      total={list.data?.total ?? 0}
      loading={list.isPending}
      error={list.isError}
      status={status}
      page={page}
      onStatus={(value) => {
        setStatus(value)
        setPage(1)
      }}
      onPage={setPage}
      onSelect={setSelected}
      onRetry={() => void list.refetch()}
    />
  )
  return (
    <>
      <SectionPageLayout stackActionsOnMobile={false}>
        <SectionPageLayout.Title>
          {admin ? t('Invoice management') : t('Invoices')}
        </SectionPageLayout.Title>
        {admin && (
          <SectionPageLayout.Description>
            {t(
              'Review paid orders, confirm receipts and deliver invoices with a complete history.'
            )}
          </SectionPageLayout.Description>
        )}
        <SectionPageLayout.Actions>
          <Button
            variant='outline'
            size='sm'
            className='rounded-full'
            disabled={list.isFetching || (!admin && eligible.isFetching)}
            onClick={() => {
              void list.refetch()
              if (!admin) void eligible.refetch()
            }}
          >
            <RefreshCw aria-hidden className='size-3.5' />
            {t('Refresh')}
          </Button>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <div className='mx-auto grid w-full max-w-[1500px] gap-4'>
            {admin && history}
            {!admin && eligible.isPending && <LoadingState />}
            {!admin && eligible.isError && (
              <ErrorState onRetry={() => void eligible.refetch()} />
            )}
            {!admin && eligible.data && (
              <>
                <InvoiceSummaryCards summary={eligible.data.summary} />
                {!eligible.data.config.invoice_enabled && (
                  <Alert>
                    <AlertDescription>
                      {t(
                        'Invoice applications are not open yet. Contact support if you need an invoice.'
                      )}
                    </AlertDescription>
                  </Alert>
                )}
                {eligible.data.config.invoice_instructions && (
                  <Alert>
                    <AlertDescription className='whitespace-pre-wrap'>
                      {eligible.data.config.invoice_instructions}
                    </AlertDescription>
                  </Alert>
                )}
                <InvoiceApplicationForm
                  key={user?.id}
                  config={eligible.data.config}
                  orders={eligible.data.items}
                  pending={create.isPending || payment.isPending}
                  history={history}
                  onSubmit={async (data, key, choice) => {
                    const previous = created.current
                    let row =
                      previous &&
                      previous.userId === user?.id &&
                      previous.key === key
                        ? previous.invoice
                        : undefined
                    if (!row) {
                      row = await create.mutateAsync({ data, key })
                      created.current = { userId: user?.id, key, invoice: row }
                    }
                    client.setQueryData(
                      ['commerce', 'invoice', user?.id, admin, row.id],
                      row
                    )
                    setSelected(row.id)
                    try {
                      if (
                        row.status === 'awaiting_fee' &&
                        BigInt(row.fee_minor) > 0n
                      ) {
                        await payment.mutateAsync({ invoice: row, choice })
                      } else {
                        toast.success(t('Application submitted'))
                      }
                      created.current = null
                    } finally {
                      await client.invalidateQueries({ queryKey: ['commerce'] })
                    }
                  }}
                />
              </>
            )}
            {selected !== null && detail.isPending && (
              <LoadingState message={t('Loading application...')} />
            )}
            {selected !== null && detail.isError && (
              <ErrorState onRetry={() => void detail.refetch()} />
            )}
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>
      {selected !== null && detail.data && (
        <InvoiceDetailDialog
          key={`${user?.id}:${detail.data.id}`}
          invoice={detail.data}
          admin={admin}
          canReview={canReview}
          pending={review.isPending || payment.isPending}
          onClose={() => setSelected(null)}
          onReview={async (input) => {
            await review.mutateAsync(input)
          }}
          onPay={async (invoice, choice) => {
            await payment.mutateAsync({ invoice, choice })
          }}
          onRefresh={async () => {
            await client.invalidateQueries({ queryKey: ['commerce'] })
          }}
        />
      )}
    </>
  )
}
