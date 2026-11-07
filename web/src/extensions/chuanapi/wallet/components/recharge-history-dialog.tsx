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
import { Receipt } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Pagination,
  PaginationContent,
  PaginationItem,
  PaginationNext,
  PaginationPrevious,
} from '@/components/ui/pagination'
import { formatMinorMoney } from '@/features/agent-platform/lib/money'
import { toIntlLocale } from '@/i18n/languages'
import { getAuthSessionEpoch } from '@/lib/auth-session'
import { formatCurrencyFromUSD } from '@/lib/currency'
import { formatQuota, formatTimestamp } from '@/lib/format'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import { formatBenefitCny } from '../../new-user-benefit/lib'
import { getAllBillingHistory, getUserBillingHistory } from '../api'
import { formatCurrency } from '../lib'

interface RechargeHistoryProps {
  admin?: boolean
}

function RechargeHistory(props: RechargeHistoryProps) {
  const { t, i18n } = useTranslation()
  const [page, setPage] = useState(1)
  const user = useAuthStore((state) => state.auth.user)
  const allowed = !!user && (!props.admin || user.role === 100)
  const query = useQuery({
    queryKey: [
      'wallet',
      'recharge-history',
      getAuthSessionEpoch(),
      props.admin === true,
      page,
    ],
    queryFn: async () => {
      const response = props.admin
        ? await getAllBillingHistory(page, 10)
        : await getUserBillingHistory(page, 10)
      const result = requireServerSuccess(response)
      if (!result.data) throw new Error('Recharge history is unavailable.')
      return result.data
    },
    enabled: allowed,
    retry: false,
    refetchOnWindowFocus: 'always',
    meta: { errorToast: false, redirectOnInternalServerError: false },
  })
  if (!allowed) return null
  if (query.isPending) return <LoadingState />
  if (query.isError) return <ErrorState onRetry={() => void query.refetch()} />

  const locale = i18n.resolvedLanguage || i18n.language
  const totalPages = Math.max(1, Math.ceil(query.data.total / 10))

  return (
    <div className='space-y-3'>
      <StaticDataTable
        data={query.data.items}
        getRowKey={(row) => row.id}
        className='overflow-x-auto'
        tableClassName='min-w-[640px]'
        emptyContent={t('No recharge records yet')}
        columns={[
          {
            id: 'order',
            header: t('Order ID'),
            cell: (row) => (
              <div className='space-y-1'>
                <div className='font-mono text-xs'>{row.trade_no}</div>
                {row.new_user_benefit && (
                  <Badge variant='secondary'>
                    {t('New user recharge benefit')}
                  </Badge>
                )}
                {row.agent_simple_topup && (
                  <Badge variant='secondary'>{t('Agent balance')}</Badge>
                )}
                {props.admin && (
                  <div className='text-muted-foreground text-xs'>
                    #{row.user_id}
                  </div>
                )}
              </div>
            ),
          },
          {
            id: 'credit',
            header: t('Total credit'),
            cell: (row) => {
              if (row.new_user_benefit) {
                if (row.new_user_benefit_credit_amount_cny) {
                  return formatBenefitCny(
                    row.new_user_benefit_credit_amount_cny,
                    locale
                  )
                }
                return row.new_user_benefit_credit_quota === undefined
                  ? '—'
                  : formatQuota(row.new_user_benefit_credit_quota)
              }
              if (row.agent_simple_topup) {
                return t('See the agent balance ledger')
              }
              const provider = row.payment_provider || row.payment_method
              if (provider === 'creem') return formatQuota(row.amount)
              if (provider === 'stripe') return formatCurrencyFromUSD(row.money)
              return formatCurrencyFromUSD(row.amount)
            },
          },
          {
            id: 'payment',
            header: t('You Pay'),
            cell: (row) => {
              if (row.new_user_benefit) {
                return formatBenefitCny(String(row.money), locale)
              }
              if (
                row.paid_currency &&
                row.paid_amount_minor &&
                row.paid_currency_exponent !== undefined
              ) {
                return formatMinorMoney(
                  row.paid_amount_minor,
                  row.paid_currency,
                  row.paid_currency_exponent,
                  toIntlLocale(locale)
                )
              }
              // Stripe Money is credited USD after group pricing, not the
              // external Price object's charge. Only receipts prove that fee.
              if ((row.payment_provider || row.payment_method) === 'stripe') {
                return '—'
              }
              return formatCurrency(row.money)
            },
          },
          {
            id: 'status',
            header: t('Status'),
            cell: (row) => {
              const labels = {
                success: 'Paid',
                pending: 'Pending',
                expired: 'Expired',
              }
              return (
                <Badge variant='outline'>
                  {t(labels[row.status] ?? row.status)}
                </Badge>
              )
            },
          },
          {
            id: 'time',
            header: t('Time'),
            cell: (row) => formatTimestamp(row.create_time),
          },
        ]}
      />
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <p className='text-muted-foreground text-xs'>
          {t('Total')}: {query.data.total}
        </p>
        <Pagination className='mx-0 w-auto'>
          <PaginationContent>
            <PaginationItem>
              <PaginationPrevious
                text={t('Previous')}
                href='#'
                aria-disabled={page <= 1}
                onClick={(event) => {
                  event.preventDefault()
                  if (page > 1) setPage(page - 1)
                }}
              />
            </PaginationItem>
            <PaginationItem>
              <span className='px-2 text-sm tabular-nums'>
                {page} / {totalPages}
              </span>
            </PaginationItem>
            <PaginationItem>
              <PaginationNext
                text={t('Next')}
                href='#'
                aria-disabled={page >= totalPages}
                onClick={(event) => {
                  event.preventDefault()
                  if (page < totalPages) setPage(page + 1)
                }}
              />
            </PaginationItem>
          </PaginationContent>
        </Pagination>
      </div>
    </div>
  )
}

export function RechargeHistoryButton(props: RechargeHistoryProps) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const user = useAuthStore((state) => state.auth.user)
  if (!user || (props.admin && user.role !== 100)) return null
  const title = props.admin ? t('All recharge records') : t('Recharge history')
  return (
    <>
      <Button
        type='button'
        variant='outline'
        size='sm'
        onClick={() => setOpen(true)}
      >
        <Receipt className='size-4' aria-hidden='true' />
        {title}
      </Button>
      <Dialog
        open={open}
        onOpenChange={setOpen}
        title={title}
        contentClassName='sm:max-w-4xl'
      >
        {open && <RechargeHistory admin={props.admin} />}
      </Dialog>
    </>
  )
}
