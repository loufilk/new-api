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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  ArrowDownToLine,
  ArrowUpRight,
  CircleCheck,
  Clock3,
  RefreshCw,
  ShieldCheck,
} from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { toIntlLocale } from '@/i18n/languages'
import { hasPermission } from '@/lib/admin-permissions'
import { formatQuota } from '@/lib/format'
import { useAuthStore } from '@/stores/auth-store'

import { formatUSDT, quotaToUSDT } from './amount'
import { getWithdrawal, getWithdrawalSummary, listWithdrawals } from './api'
import { CreateWithdrawalDialog } from './create-dialog'
import { WithdrawalDetailDialog } from './detail-dialog'
import { WithdrawalSettings } from './settings'
import {
  withdrawalStatuses,
  withdrawalBadgeVariant,
  type Withdrawal,
} from './types'

export function ReferralWithdrawals({ admin = false }: { admin?: boolean }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.language)
  const user = useAuthStore((state) => state.auth.user)
  const client = useQueryClient()
  const [status, setStatus] = useState('')
  const [page, setPage] = useState(1)
  const [createOpen, setCreateOpen] = useState(false)
  const [selected, setSelected] = useState<number | null>(null)
  const summary = useQuery({
    queryKey: ['referral-withdrawals', 'summary', user?.id],
    queryFn: getWithdrawalSummary,
    enabled: !admin,
    refetchInterval: 30_000,
  })
  const list = useQuery({
    queryKey: ['referral-withdrawals', 'list', admin, user?.id, status, page],
    queryFn: () => listWithdrawals(admin, status, page),
    refetchInterval: 30_000,
  })
  const detail = useQuery({
    queryKey: ['referral-withdrawals', 'detail', admin, user?.id, selected],
    queryFn: () => getWithdrawal(admin, selected ?? 0),
    enabled: selected !== null,
  })
  const refresh = async () => {
    await Promise.all([
      client.invalidateQueries({ queryKey: ['referral-withdrawals'] }),
      client.invalidateQueries({ queryKey: ['wallet'] }),
      client.invalidateQueries({ queryKey: ['affiliate', 'self'] }),
    ])
  }
  const pages = Math.max(1, Math.ceil((list.data?.total ?? 0) / 20))
  const columns: StaticDataTableColumn<Withdrawal>[] = [
    {
      id: 'request',
      header: t('Request'),
      cell: (row) => (
        <div className='space-y-1'>
          <p className='font-medium'>#{row.id}</p>
          <p className='text-muted-foreground text-xs'>
            {admin
              ? t('User #{{id}}', { id: row.user_id })
              : new Date(row.created_at * 1000).toLocaleDateString(locale)}
          </p>
        </div>
      ),
    },
    {
      id: 'destination',
      header: t('Destination'),
      cell: (row) => (
        <div className='max-w-48 space-y-1'>
          <Badge variant='outline'>{row.network}</Badge>
          <p
            className='text-muted-foreground truncate font-mono text-xs'
            title={row.address}
          >
            {row.address}
          </p>
        </div>
      ),
    },
    {
      id: 'amount',
      header: t('You receive'),
      cell: (row) => (
        <div className='space-y-1'>
          <p className='font-semibold tabular-nums'>
            {formatUSDT(row.net_usdt_micro)} USDT
          </p>
          <p className='text-muted-foreground text-xs'>
            {t('Fee')}: {formatUSDT(row.fee_usdt_micro)} USDT
          </p>
        </div>
      ),
    },
    {
      id: 'status',
      header: t('Status'),
      cell: (row) => (
        <Badge variant={withdrawalBadgeVariant(row.status)}>
          {t(withdrawalStatuses[row.status])}
        </Badge>
      ),
    },
    {
      id: 'actions',
      header: t('Actions'),
      cell: (row) => (
        <Button size='sm' variant='outline' onClick={() => setSelected(row.id)}>
          {t('View details')}
          <ArrowUpRight aria-hidden />
        </Button>
      ),
    },
  ]
  return (
    <div id={admin ? undefined : 'referral-withdrawals'} className='grid gap-5'>
      {!admin && (
        <Card>
          <CardHeader className='flex flex-wrap items-start justify-between gap-3 sm:flex-row'>
            <div>
              <CardTitle className='flex items-center gap-2'>
                <ArrowDownToLine aria-hidden className='size-5' />
                {t('Withdraw invitation rewards')}
              </CardTitle>
              <CardDescription className='mt-2'>
                {t(
                  'Convert released referral rewards into USDT with a visible review and payment history.'
                )}
              </CardDescription>
            </div>
            <Button
              disabled={!summary.data?.eligible || summary.isError}
              onClick={() => setCreateOpen(true)}
            >
              {t('Request a withdrawal')}
            </Button>
          </CardHeader>
          <CardContent className='space-y-5'>
            {summary.isLoading && <LoadingState />}
            {summary.isError && (
              <ErrorState onRetry={() => void summary.refetch()} />
            )}
            {summary.data && (
              <>
                <div className='grid gap-3 sm:grid-cols-3'>
                  {[
                    {
                      label: 'Available for withdrawal',
                      icon: CircleCheck,
                      amount: `${quotaToUSDT(Math.max(0, summary.data.available_quota), summary.data.config.quota_per_usdt)} USDT`,
                      detail: formatQuota(summary.data.available_quota),
                    },
                    {
                      label: 'Reserved withdrawals',
                      icon: Clock3,
                      amount: `${quotaToUSDT(summary.data.reserved_quota, summary.data.config.quota_per_usdt)} USDT`,
                      detail: t(
                        'Reserved requests cannot be transferred to the wallet.'
                      ),
                    },
                    {
                      label: 'Account usage requirement',
                      icon: ShieldCheck,
                      amount: `${formatQuota(summary.data.consumed_quota)} / ${formatQuota(summary.data.config.min_consumed_quota)}`,
                      detail:
                        summary.data.consumed_quota >=
                        summary.data.config.min_consumed_quota
                          ? t('Usage requirement met')
                          : t(
                              'Keep using the service to meet this requirement.'
                            ),
                    },
                  ].map(({ label, icon: Icon, amount, detail }) => (
                    <div className='rounded-xl border p-4' key={label}>
                      <div className='text-muted-foreground flex items-center gap-2 text-xs'>
                        <Icon aria-hidden className='size-4' />
                        {t(label)}
                      </div>
                      <p className='mt-3 text-lg font-semibold break-all tabular-nums'>
                        {amount}
                      </p>
                      <p className='text-muted-foreground mt-2 text-xs'>
                        {detail}
                      </p>
                    </div>
                  ))}
                </div>
                {!summary.data.config.enabled && (
                  <Alert>
                    <AlertDescription>
                      {t('Referral withdrawals are currently closed')}
                    </AlertDescription>
                  </Alert>
                )}
                <div className='flex flex-wrap gap-x-6 gap-y-2 text-xs'>
                  <span className='text-muted-foreground'>
                    {t('Minimum withdrawal')}:{' '}
                    <span className='text-foreground font-medium'>
                      {quotaToUSDT(
                        summary.data.config.min_withdrawal_quota,
                        summary.data.config.quota_per_usdt
                      )}{' '}
                      USDT
                    </span>
                  </span>
                  {[
                    ['BSC', summary.data.config.bsc_fee_bps],
                    ['POLYGON', summary.data.config.polygon_fee_bps],
                    ['TRC20', summary.data.config.trc20_fee_bps],
                  ].map(([network, bps]) => (
                    <span key={network} className='text-muted-foreground'>
                      {network} {t('Fee')}:{' '}
                      <span className='text-foreground font-medium'>
                        {Number(bps) / 100}%
                      </span>
                    </span>
                  ))}
                </div>
                {summary.data.config.instructions && (
                  <p className='text-muted-foreground text-sm whitespace-pre-wrap'>
                    {summary.data.config.instructions}
                  </p>
                )}
              </>
            )}
          </CardContent>
        </Card>
      )}
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <h2 className='text-lg font-semibold'>
          {admin ? t('Withdrawal requests') : t('Withdrawal history')}
        </h2>
        <div className='flex items-center gap-2'>
          <NativeSelect
            aria-label={t('Withdrawal status')}
            value={status}
            onChange={(e) => {
              setStatus(e.target.value)
              setPage(1)
            }}
          >
            <NativeSelectOption value=''>
              {t('All statuses')}
            </NativeSelectOption>
            {Object.entries(withdrawalStatuses).map(([value, label]) => (
              <NativeSelectOption key={value} value={value}>
                {t(label)}
              </NativeSelectOption>
            ))}
          </NativeSelect>
          <Button
            variant='outline'
            size='icon'
            aria-label={t('Refresh withdrawals')}
            onClick={() => void refresh()}
          >
            <RefreshCw aria-hidden />
          </Button>
        </div>
      </div>
      {list.isLoading && <LoadingState />}
      {list.isError && <ErrorState onRetry={() => void list.refetch()} />}
      {list.data && (
        <StaticDataTable
          columns={columns}
          data={list.data.items}
          getRowKey={(row) => row.id}
          emptyContent={
            <EmptyState
              icon={ArrowDownToLine}
              title={t('No withdrawal requests')}
              description={t(
                'Submitted withdrawals and recorded payments will appear here.'
              )}
            />
          }
        />
      )}
      {pages > 1 && (
        <div className='flex justify-end gap-3'>
          <Button
            variant='outline'
            disabled={page <= 1}
            onClick={() => setPage(page - 1)}
          >
            {t('Previous')}
          </Button>
          <span className='text-muted-foreground py-2 text-sm'>
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
      {createOpen && summary.data && (
        <CreateWithdrawalDialog
          key={user?.id}
          summary={summary.data}
          onClose={() => setCreateOpen(false)}
          onCreated={async (row) => {
            setCreateOpen(false)
            setSelected(row.id)
            toast.success(t('Withdrawal request submitted'))
            await refresh()
          }}
        />
      )}
      {selected !== null && detail.isLoading && <LoadingState />}
      {selected !== null && detail.isError && (
        <ErrorState onRetry={() => void detail.refetch()} />
      )}
      {selected !== null && detail.data && (
        <WithdrawalDetailDialog
          key={`${user?.id}:${detail.data.id}`}
          row={detail.data}
          admin={admin}
          onClose={() => setSelected(null)}
          onChanged={refresh}
        />
      )}
    </div>
  )
}

export function ReferralWithdrawalsAdminPage() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const canRead = hasPermission(user, 'referral_withdrawals', 'read')
  const canConfigure = hasPermission(
    user,
    'referral_withdrawal_settings',
    'read'
  )
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Referral withdrawals')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Description>
        {t(
          'Review ordinary referral withdrawals and record manual payments with a complete audit trail.'
        )}
      </SectionPageLayout.Description>
      <SectionPageLayout.Content>
        <div className='mx-auto w-full max-w-7xl'>
          <Tabs defaultValue={canRead ? 'requests' : 'settings'}>
            <TabsList className='mb-5'>
              {canRead && (
                <TabsTrigger value='requests'>
                  {t('Withdrawal requests')}
                </TabsTrigger>
              )}
              {canConfigure && (
                <TabsTrigger value='settings'>
                  {t('Withdrawal settings')}
                </TabsTrigger>
              )}
            </TabsList>
            {canRead && (
              <TabsContent value='requests'>
                <ReferralWithdrawals admin />
              </TabsContent>
            )}
            {canConfigure && (
              <TabsContent value='settings'>
                <WithdrawalSettings />
              </TabsContent>
            )}
          </Tabs>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
