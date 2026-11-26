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
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { Receipt } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
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
import { getAllBillingHistory, getRechargeHistory } from '../api'
import { formatCurrency } from '../lib'
import type { RechargeHistoryItem, RechargeHistoryResponse } from '../types'

const paymentMethodLabels: ReadonlyMap<string, string> = new Map([
  ['alipay', 'Alipay'],
  ['wxpay', 'WeChat Pay'],
  ['stripe', 'Stripe'],
  ['waffo', 'Waffo'],
  ['waffo_pancake', 'Waffo Pancake'],
  ['creem', 'Creem'],
  ['balance', 'Balance'],
])

const usdtStatusLabels = {
  creating: 'Preparing USDT order',
  pending: 'Awaiting transfer',
  confirming: 'Confirming on chain',
  paid: 'Credited',
  expired: 'Expired',
  review: 'Manual review',
  failed: 'Failed',
} as const

const usdtNetworks: ReadonlyMap<string, string> = new Map([
  ['bsc', 'BSC'],
  ['polygon', 'Polygon PoS'],
  ['tron', 'TRC20'],
])

interface RechargeHistoryProps {
  admin?: boolean
}

type RechargeHistoryView = Omit<RechargeHistoryResponse, 'items'> & {
  items: (RechargeHistoryItem & { adminUserId?: number })[]
}

function RechargeHistory(props: RechargeHistoryProps) {
  const { t, i18n } = useTranslation()
  const [page, setPage] = useState(1)
  const [draftOrderNo, setDraftOrderNo] = useState('')
  const [orderNo, setOrderNo] = useState('')
  const [method, setMethod] = useState('')
  const user = useAuthStore((state) => state.auth.user)
  const allowed = !!user && (!props.admin || user.role === 100)
  const query = useQuery({
    queryKey: [
      'wallet',
      'recharge-history',
      getAuthSessionEpoch(),
      props.admin === true,
      page,
      orderNo,
      method,
    ],
    queryFn: async ({ signal }): Promise<RechargeHistoryView> => {
      if (props.admin) {
        const result = requireServerSuccess(
          await getAllBillingHistory(page, 10)
        )
        if (!result.data) throw new Error('Recharge history is unavailable.')
        return {
          ...result.data,
          items: result.data.items.map((fiat) => ({
            order_no: fiat.trade_no,
            created_at: fiat.create_time,
            fiat,
            adminUserId: fiat.user_id,
          })),
          methods: [],
        }
      }
      const result = requireServerSuccess(
        await getRechargeHistory(page, 10, orderNo, method, signal)
      )
      if (!result.data) throw new Error('Recharge history is unavailable.')
      return result.data
    },
    placeholderData: keepPreviousData,
    enabled: allowed,
    retry: false,
    refetchOnWindowFocus: 'always',
    meta: { errorToast: false, redirectOnInternalServerError: false },
  })
  if (!allowed) return null
  const locale = i18n.resolvedLanguage || i18n.language
  const methods = query.data?.methods ?? []
  const methodLabel = (value: string) =>
    t(paymentMethodLabels.get(value) ?? (value || 'Unknown'))
  const totalPages = Math.max(1, Math.ceil((query.data?.total ?? 0) / 10))

  return (
    <div className='space-y-3'>
      {!props.admin && (
        <div className='flex flex-wrap items-end gap-2'>
          <form
            className='flex min-w-0 flex-wrap items-end gap-2'
            onSubmit={(event) => {
              event.preventDefault()
              setOrderNo(draftOrderNo.trim())
              setPage(1)
            }}
          >
            <label className='grid min-w-0 gap-1 text-sm'>
              {t('Order ID')}
              <Input
                type='search'
                maxLength={255}
                value={draftOrderNo}
                onChange={(event) => setDraftOrderNo(event.target.value)}
                placeholder={t('Enter the full order number')}
              />
            </label>
            <Button type='submit' variant='outline'>
              {t('Search')}
            </Button>
          </form>
          <label className='grid gap-1 text-sm'>
            {t('Payment method')}
            <NativeSelect
              className='min-w-32'
              value={method}
              onChange={(event) => {
                setMethod(event.target.value)
                setPage(1)
              }}
            >
              <NativeSelectOption value=''>
                {t('All payment methods')}
              </NativeSelectOption>
              {methods.map((value) => (
                <NativeSelectOption key={value} value={value}>
                  {value === 'usdt' ? 'USDT' : methodLabel(value.slice(5))}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </label>
          {(orderNo || draftOrderNo || method) && (
            <Button
              type='button'
              variant='ghost'
              onClick={() => {
                setDraftOrderNo('')
                setOrderNo('')
                setMethod('')
                setPage(1)
              }}
            >
              {t('Clear filters')}
            </Button>
          )}
        </div>
      )}
      {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
      {!query.isError && (query.isPlaceholderData || !query.data) && (
        <LoadingState />
      )}
      {!query.isError && !query.isPlaceholderData && query.data && (
        <>
          <StaticDataTable
            data={query.data.items}
            getRowKey={(row) => `${row.usdt ? 'usdt' : 'fiat'}:${row.order_no}`}
            className='overflow-x-auto'
            tableClassName='min-w-[680px]'
            emptyContent={t(
              orderNo || method
                ? 'No matching results'
                : 'No recharge records yet'
            )}
            columns={[
              {
                id: 'order',
                header: t('Order ID'),
                cell: (row) => (
                  <div className='space-y-1'>
                    <div className='font-mono text-xs'>{row.order_no}</div>
                    {row.fiat?.new_user_benefit && (
                      <Badge variant='secondary'>
                        {t('New user recharge benefit')}
                      </Badge>
                    )}
                    {row.fiat?.agent_simple_topup && (
                      <Badge variant='secondary'>{t('Agent balance')}</Badge>
                    )}
                    {props.admin && row.adminUserId !== undefined && (
                      <div className='text-muted-foreground text-xs'>
                        #{row.adminUserId}
                      </div>
                    )}
                  </div>
                ),
              },
              {
                id: 'credit',
                header: t('Total credit'),
                cell: ({ fiat: row, usdt }) => {
                  if (usdt?.wallet_credit_cny) {
                    return formatBenefitCny(usdt.wallet_credit_cny, locale)
                  }
                  if (!row) return '—'
                  if (row.new_user_benefit) {
                    if (
                      row.new_user_benefit_credited_amount_cny !== undefined
                    ) {
                      return formatBenefitCny(
                        row.new_user_benefit_credited_amount_cny,
                        locale
                      )
                    }
                    if (row.new_user_benefit_credited_quota !== undefined) {
                      return formatQuota(row.new_user_benefit_credited_quota)
                    }
                    if (
                      row.status === 'success' &&
                      (row.new_user_benefit_reward_status === 'pending' ||
                        row.new_user_benefit_reward_status === 'denied')
                    ) {
                      return formatBenefitCny(String(row.money), locale)
                    }
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
                  if (provider === 'stripe') {
                    return formatCurrencyFromUSD(row.money)
                  }
                  return formatCurrencyFromUSD(row.amount)
                },
              },
              {
                id: 'payment',
                header: t('You Pay'),
                cell: ({ fiat: row, usdt }) => {
                  if (usdt?.quote_usdt) {
                    return (
                      <div>
                        <div>{usdt.quote_usdt} USDT</div>
                        <small className='text-muted-foreground'>
                          {t('USDT order quote')}
                        </small>
                      </div>
                    )
                  }
                  if (!row) return '—'
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
                  if (
                    (row.payment_provider || row.payment_method) === 'stripe'
                  ) {
                    return '—'
                  }
                  return formatCurrency(row.money)
                },
              },
              {
                id: 'method',
                header: t('Payment method'),
                cell: ({ fiat, usdt }) => {
                  if (!usdt) return methodLabel(fiat?.payment_method ?? '')
                  const network = usdtNetworks.get(usdt.network)
                  return network ? `USDT · ${network}` : 'USDT'
                },
              },
              {
                id: 'status',
                header: t('Status'),
                cell: ({ fiat: row, usdt }) => {
                  if (usdt) {
                    return (
                      <Badge variant='outline'>
                        {t(usdtStatusLabels[usdt.status] ?? 'Unknown')}
                      </Badge>
                    )
                  }
                  if (!row) return '—'
                  if (
                    row.new_user_benefit &&
                    row.status === 'success' &&
                    row.new_user_benefit_reward_status === 'pending'
                  ) {
                    return (
                      <Badge variant='outline'>
                        {t('Benefit bonus pending verification')}
                      </Badge>
                    )
                  }
                  if (
                    row.new_user_benefit &&
                    row.status === 'success' &&
                    row.new_user_benefit_reward_status === 'denied'
                  ) {
                    return (
                      <Badge variant='outline'>
                        {t('Benefit bonus not issued')}
                      </Badge>
                    )
                  }
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
                cell: (row) => formatTimestamp(row.created_at),
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
                    aria-label={t('Go to previous page')}
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
                    aria-label={t('Go to next page')}
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
        </>
      )}
    </div>
  )
}

type RechargeHistoryButtonProps = RechargeHistoryProps & {
  open?: boolean
  defaultOpen?: boolean
  onOpenChange?: (open: boolean) => void
}

export function RechargeHistoryButton(props: RechargeHistoryButtonProps) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  if (!user || (props.admin && user.role !== 100)) return null
  const title = props.admin ? t('All recharge records') : t('Recharge history')
  return (
    <Dialog
      open={props.open}
      defaultOpen={props.defaultOpen}
      onOpenChange={props.onOpenChange}
      title={title}
      contentClassName='sm:max-w-4xl'
      trigger={
        <Button type='button' variant='outline' size='sm'>
          <Receipt className='size-4' aria-hidden='true' />
          {title}
        </Button>
      }
    >
      <RechargeHistory
        key={`${user.id}:${getAuthSessionEpoch()}`}
        admin={props.admin}
      />
    </Dialog>
  )
}
