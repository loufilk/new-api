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
import { ClipboardCheck, FileText, Plus, RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { toIntlLocale } from '@/i18n/languages'
import { hasPermission } from '@/lib/admin-permissions'
import { formatNumber } from '@/lib/format'
import { useAuthStore } from '@/stores/auth-store'

import {
  createInvoice,
  getEligibleInvoiceOrders,
  getInvoice,
  listInvoices,
  reviewInvoice,
} from './api'
import {
  ApplicationStatus,
  InvoiceMoney,
} from './components/application-status'
import { InvoiceCreateDialog } from './components/invoice-create-dialog'
import { InvoiceDetailDialog } from './components/invoice-detail-dialog'
import type { ApplicationReview, InvoiceInput } from './types'

export function InvoicesPage(props: { admin?: boolean }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const user = useAuthStore((state) => state.auth.user)
  const admin = props.admin === true
  const canReview = admin && hasPermission(user, 'commerce_invoices', 'review')
  const client = useQueryClient()
  const [status, setStatus] = useState('')
  const [page, setPage] = useState(1)
  const [createOpen, setCreateOpen] = useState(false)
  const [selected, setSelected] = useState<number | null>(null)
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
  const create = useMutation({
    mutationFn: (input: { data: InvoiceInput; key: string }) =>
      createInvoice(input.data, input.key),
    onSuccess: async (row) => {
      setCreateOpen(false)
      setSelected(row.id)
      toast.success(t('Application submitted'))
      await client.invalidateQueries({ queryKey: ['commerce'] })
    },
  })
  const review = useMutation({
    mutationFn: (input: ApplicationReview) =>
      reviewInvoice(admin, selected as number, input),
    onSuccess: async () => {
      toast.success(t('Application updated'))
      await client.invalidateQueries({ queryKey: ['commerce'] })
    },
  })
  const pages = Math.max(1, Math.ceil((list.data?.total ?? 0) / 20))

  return (
    <>
      <SectionPageLayout stackActionsOnMobile>
        <SectionPageLayout.Title>
          {admin ? t('Invoice management') : t('Invoices')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Description>
          {admin
            ? t(
                'Review paid orders, confirm receipts and deliver invoices with a complete history.'
              )
            : t(
                'Apply for invoices from settled recharge orders and follow their progress.'
              )}
        </SectionPageLayout.Description>
        <SectionPageLayout.Actions>
          <Button
            variant='outline'
            aria-label={t('Refresh')}
            onClick={() => {
              void list.refetch()
              if (!admin) void eligible.refetch()
            }}
          >
            <RefreshCw aria-hidden />
          </Button>
          {!admin && (
            <Button
              disabled={!eligible.data?.config.invoice_enabled}
              onClick={() => setCreateOpen(true)}
            >
              <Plus aria-hidden />
              {t('Apply for an invoice')}
            </Button>
          )}
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <div className='mx-auto grid w-full max-w-7xl gap-5'>
            {!admin && eligible.isError && (
              <ErrorState onRetry={() => void eligible.refetch()} />
            )}
            {!admin && eligible.data && (
              <Card>
                <CardContent className='flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between'>
                  <div className='flex items-start gap-3'>
                    <div className='bg-primary/10 text-primary rounded-xl p-3'>
                      <ClipboardCheck aria-hidden className='size-5' />
                    </div>
                    <div>
                      <h2 className='font-semibold'>{t('Eligible orders')}</h2>
                      <p className='text-muted-foreground mt-1 text-sm'>
                        {t(
                          '{{count}} eligible orders · {{currency}} · last {{days}} days',
                          {
                            count: eligible.data.items.length,
                            currency: eligible.data.config.invoice_currency,
                            days: eligible.data.config.invoice_window_days,
                          }
                        )}
                      </p>
                    </div>
                  </div>
                  <div className='text-muted-foreground text-sm'>
                    {t('General invoice fee')}:{' '}
                    {formatNumber(
                      eligible.data.config.general_fee_bps / 100,
                      locale
                    )}
                    %
                  </div>
                </CardContent>
              </Card>
            )}
            {!admin &&
              eligible.data &&
              !eligible.data.config.invoice_enabled && (
                <Alert>
                  <AlertDescription>
                    {t(
                      'Invoice applications are not open yet. Contact support if you need an invoice.'
                    )}
                  </AlertDescription>
                </Alert>
              )}
            {!admin && eligible.data?.config.invoice_instructions && (
              <Alert>
                <AlertDescription className='whitespace-pre-wrap'>
                  {eligible.data.config.invoice_instructions}
                </AlertDescription>
              </Alert>
            )}
            <div className='flex flex-wrap items-center justify-between gap-3'>
              <h2 className='text-lg font-semibold'>
                {admin ? t('Application queue') : t('Application history')}
              </h2>
              <NativeSelect
                value={status}
                aria-label={t('Application status')}
                onChange={(event) => {
                  setStatus(event.target.value)
                  setPage(1)
                }}
              >
                <NativeSelectOption value=''>
                  {t('All statuses')}
                </NativeSelectOption>
                <NativeSelectOption value='submitted'>
                  {t('Pending review')}
                </NativeSelectOption>
                <NativeSelectOption value='awaiting_fee'>
                  {t('Awaiting service fee')}
                </NativeSelectOption>
                <NativeSelectOption value='approved'>
                  {t('Approved')}
                </NativeSelectOption>
                <NativeSelectOption value='issued'>
                  {t('Invoice issued')}
                </NativeSelectOption>
                <NativeSelectOption value='rejected'>
                  {t('Rejected')}
                </NativeSelectOption>
                <NativeSelectOption value='cancelled'>
                  {t('Cancelled')}
                </NativeSelectOption>
              </NativeSelect>
            </div>
            {list.isLoading && <LoadingState />}
            {list.isError && <ErrorState onRetry={() => void list.refetch()} />}
            {list.data?.items.length === 0 && (
              <EmptyState
                bordered
                icon={FileText}
                title={t('No invoice applications')}
                description={t(
                  'Your submitted applications and issued invoices will appear here.'
                )}
              />
            )}
            <div className='grid gap-3 md:grid-cols-2'>
              {list.data?.items.map((row) => (
                <Card key={row.id}>
                  <CardContent className='space-y-4'>
                    <div className='flex items-start justify-between gap-3'>
                      <div className='min-w-0'>
                        <h3 className='truncate font-medium'>{row.title}</h3>
                        <p className='text-muted-foreground mt-1 text-xs'>
                          #{row.id}
                          {admin &&
                            ` · ${t('User #{{id}}', { id: row.user_id })}`}
                        </p>
                      </div>
                      <ApplicationStatus status={row.status} />
                    </div>
                    <div className='flex flex-wrap items-end justify-between gap-3'>
                      <div>
                        <p className='text-lg font-semibold'>
                          <InvoiceMoney
                            minor={row.amount_minor}
                            currency={row.currency}
                            exponent={row.currency_exponent}
                          />
                        </p>
                        <p className='text-muted-foreground mt-1 text-xs'>
                          {new Date(row.created_at * 1000).toLocaleString(
                            locale
                          )}
                        </p>
                      </div>
                      <Button
                        variant='outline'
                        onClick={() => setSelected(row.id)}
                      >
                        {t('View details')}
                      </Button>
                    </div>
                  </CardContent>
                </Card>
              ))}
            </div>
            {pages > 1 && (
              <div className='flex items-center justify-end gap-3'>
                <Button
                  variant='outline'
                  disabled={page === 1}
                  onClick={() => setPage(page - 1)}
                >
                  {t('Previous')}
                </Button>
                <span className='text-muted-foreground text-sm'>
                  {t('Page {{page}} of {{total}}', { page, total: pages })}
                </span>
                <Button
                  variant='outline'
                  disabled={page >= pages}
                  onClick={() => setPage(page + 1)}
                >
                  {t('Next')}
                </Button>
              </div>
            )}
            {selected !== null && detail.isLoading && (
              <LoadingState message={t('Loading application...')} />
            )}
            {selected !== null && detail.isError && (
              <ErrorState onRetry={() => void detail.refetch()} />
            )}
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>
      {createOpen && eligible.data && (
        <InvoiceCreateDialog
          key={user?.id}
          config={eligible.data.config}
          orders={eligible.data.items}
          pending={create.isPending}
          onClose={() => setCreateOpen(false)}
          onSubmit={async (data, key) => {
            await create.mutateAsync({ data, key })
          }}
        />
      )}
      {selected !== null && detail.data && (
        <InvoiceDetailDialog
          key={`${user?.id}:${detail.data.id}`}
          invoice={detail.data}
          admin={admin}
          canReview={canReview}
          pending={review.isPending}
          onClose={() => setSelected(null)}
          onReview={async (input) => {
            await review.mutateAsync(input)
          }}
        />
      )}
    </>
  )
}
