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
import {
  ArrowUpRight,
  CircleDollarSign,
  HandCoins,
  Ticket,
  WalletCards,
} from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { extensionWalletPage } from '@/extensions'
import { REDEMPTION_STATUSES } from '@/features/redemption-codes/constants'
import { isRedemptionExpired } from '@/features/redemption-codes/lib'
import { formatTimestampToDate } from '@/lib/format'
import { useAuthStore } from '@/stores/auth-store'

import { getPlatform } from './api'
import { SimpleCodeForm } from './components/simple-code-form'
import { SimpleHistory } from './components/simple-history'
import { SimpleIncomeTransferForm } from './components/simple-income-transfer-form'
import { SimplePaymentForm } from './components/simple-payment-form'
import { SimpleRechargeForm } from './components/simple-recharge-form'
import { formatAgentCash, formatAgentQuota as formatQuota } from './lib/money'
import {
  getSimpleAgentBalance,
  getSimpleAgentCodes,
  type SimpleCodeRecord,
} from './simple-api'
import type { PlatformSelf } from './types'

export function AgentPortal() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const userId = user?.id
  const self = useQuery({
    queryKey: ['agent-platform', userId, 'self'],
    queryFn: ({ signal }) =>
      getPlatform<PlatformSelf>('/self', undefined, signal),
  })
  const balance = useQuery({
    queryKey: ['agent-platform', userId, 'simple-balance'],
    queryFn: ({ signal }) => getSimpleAgentBalance(signal),
    enabled: !!userId,
  })
  const recent = useQuery({
    queryKey: ['agent-platform', userId, 'recent-redemptions'],
    queryFn: async () => {
      return getSimpleAgentCodes({
        p: 1,
        page_size: 5,
        agent_user_id: userId,
      })
    },
    enabled: !!userId,
  })
  const RewardsPanel = extensionWalletPage().referralPanel
  const [simpleCodeOpen, setSimpleCodeOpen] = useState(false)
  const [simpleRechargeOpen, setSimpleRechargeOpen] = useState(false)
  const [repaymentOpen, setRepaymentOpen] = useState(false)
  const [incomeTransferOpen, setIncomeTransferOpen] = useState(false)
  const [simplePaymentOpen, setSimplePaymentOpen] = useState(false)
  const [billsOpen, setBillsOpen] = useState(false)

  if (self.isPending || balance.isPending) {
    return <LoadingState />
  }
  if (self.isError || balance.isError || !self.data || !balance.data) {
    return (
      <ErrorState
        onRetry={() => {
          void self.refetch()
          void balance.refetch()
        }}
      />
    )
  }

  const account = balance.data
  const transferablePayment = Math.max(
    0,
    account.payment_available_quota -
      account.payment_debt_quota -
      account.amount_due_quota
  )
  const redemptions = recent.data?.items ?? []
  let recentContent
  if (recent.isLoading) {
    recentContent = <LoadingState inline />
  } else if (redemptions.length === 0) {
    recentContent = (
      <EmptyState
        className='min-h-32'
        title={t('No redemption codes yet')}
        description={t(
          'Open the code center to issue your first customer code.'
        )}
      />
    )
  } else {
    recentContent = (
      <div className='divide-border overflow-hidden rounded-lg border'>
        {redemptions.map((row) => (
          <RecentRedemption key={row.id} row={row} />
        ))}
      </div>
    )
  }
  return (
    <>
      <SectionPageLayout>
        <SectionPageLayout.Title>{t('Agent Overview')}</SectionPageLayout.Title>
        <SectionPageLayout.Content>
          <div className='mx-auto flex w-full max-w-[1440px] flex-col gap-4'>
            <div className='grid gap-4 xl:grid-cols-[minmax(0,1.15fr)_minmax(360px,0.85fr)]'>
              <Card className='overflow-hidden border-sky-200/70 bg-gradient-to-br from-sky-950 via-sky-900 to-cyan-900 py-0 text-white shadow-lg'>
                <CardHeader className='border-b border-white/10 py-5'>
                  <div className='flex flex-wrap items-start justify-between gap-3'>
                    <div>
                      <CardTitle className='text-xl'>
                        {t('Agent balance')}
                      </CardTitle>
                      <CardDescription className='text-sky-100/75'>
                        {t(
                          'Use this balance to issue redemption codes for customers.'
                        )}
                      </CardDescription>
                    </div>
                    <WalletCards
                      className='size-8 text-cyan-200'
                      aria-hidden='true'
                    />
                  </div>
                </CardHeader>
                <CardContent className='space-y-5 p-5'>
                  <dl className='grid grid-cols-2 gap-4 sm:grid-cols-3'>
                    <Metric
                      label={t('Agent balance')}
                      value={formatQuota(account.available_quota)}
                      emphasis
                    />
                    <Metric
                      label={t('Available credit')}
                      value={formatAgentCash(account.available_credit_quota)}
                    />
                    <Metric
                      label={t('Amount due')}
                      value={formatAgentCash(account.amount_due_quota)}
                    />
                    <Metric
                      label={t('Payment income available')}
                      value={formatQuota(account.payment_available_quota)}
                    />
                  </dl>
                  <Button
                    className='border-white/25 bg-transparent text-white hover:bg-white/10'
                    size='sm'
                    variant='outline'
                    onClick={() => setIncomeTransferOpen(true)}
                    disabled={
                      account.status !== 'active' || transferablePayment <= 0
                    }
                  >
                    {t('Transfer payment income to agent balance')}
                  </Button>
                  {(account.amount_due_quota > 0 ||
                    account.payment_debt_quota > 0) && (
                    <p className='text-xs text-sky-100/75'>
                      {t(
                        'Released income repays outstanding debt first. You can transfer the remainder to agent balance.'
                      )}
                    </p>
                  )}
                  <div className='flex flex-wrap gap-2'>
                    <Button
                      className='bg-white text-sky-950 hover:bg-sky-100'
                      onClick={() => setSimpleCodeOpen(true)}
                      disabled={account.status !== 'active'}
                    >
                      <Ticket className='size-4' />
                      {t('Issue directly')}
                    </Button>
                    <Button
                      className='border-white/25 bg-transparent text-white hover:bg-white/10'
                      variant='outline'
                      onClick={() => setSimpleRechargeOpen(true)}
                      disabled={account.status !== 'active'}
                    >
                      <CircleDollarSign className='size-4' />
                      {t('Recharge agent balance')}
                    </Button>
                    <Button
                      className='border-white/25 bg-transparent text-white hover:bg-white/10'
                      variant='outline'
                      disabled={
                        account.amount_due_quota <= 0 ||
                        account.status === 'cancelled'
                      }
                      onClick={() => setRepaymentOpen(true)}
                    >
                      {t('Repay amount due')}
                    </Button>
                    <Button
                      className='border-white/25 bg-transparent text-white hover:bg-white/10'
                      variant='outline'
                      onClick={() => setSimplePaymentOpen(true)}
                      disabled={account.status !== 'active'}
                    >
                      <HandCoins className='size-4' />
                      {t('Generate customer payment code')}
                    </Button>
                  </div>
                  {(account.overdue_quota ?? 0) > 0 && (
                    <p
                      role='status'
                      className='rounded-md border border-amber-200/40 bg-amber-50/10 p-3 text-sm text-amber-100'
                    >
                      {t(
                        'Overdue amount: {{amount}}. Please repay or contact support.',
                        { amount: formatAgentCash(account.overdue_quota ?? 0) }
                      )}
                    </p>
                  )}
                  {account.amount_due_quota > 0 && (
                    <Button
                      size='sm'
                      variant='outline'
                      className='border-white/25 bg-transparent text-white hover:bg-white/10'
                      onClick={() => setBillsOpen(true)}
                    >
                      {t('Bills and reminders')}
                    </Button>
                  )}
                  <p className='text-xs text-sky-100/70'>
                    {t(
                      'Direct codes use agent balance or credit. Customer payment codes do not use agent balance; their profit is frozen for five days.'
                    )}
                  </p>
                </CardContent>
              </Card>

              <div className='min-w-0'>
                {RewardsPanel ? <RewardsPanel /> : null}
              </div>
            </div>

            <div className='grid gap-4 lg:grid-cols-[minmax(0,1fr)_320px]'>
              <Card>
                <CardHeader className='flex flex-row items-center justify-between gap-3'>
                  <div>
                    <CardTitle>{t('Recent redemption codes')}</CardTitle>
                    <CardDescription>
                      {t('Only the latest codes are shown here.')}
                    </CardDescription>
                  </div>
                  <Button
                    size='sm'
                    variant='outline'
                    render={
                      <Link
                        to='/redemption-codes'
                        search={{ view: 'records' }}
                      />
                    }
                  >
                    {t('View all')}
                    <ArrowUpRight className='size-4' />
                  </Button>
                </CardHeader>
                <CardContent>{recentContent}</CardContent>
              </Card>

              <Card>
                <CardHeader>
                  <CardTitle>{t('Agent status')}</CardTitle>
                  <CardDescription>
                    {t('Your ordinary account and wallet remain available.')}
                  </CardDescription>
                </CardHeader>
                <CardContent className='space-y-3 text-sm'>
                  <div className='flex items-center justify-between'>
                    <span className='text-muted-foreground'>{t('Status')}</span>
                    <span className='font-medium'>
                      {{
                        active: t('Active'),
                        cancelled: t('Cancelled'),
                        paused: t('Paused'),
                      }[account.status] ?? account.status}
                    </span>
                  </div>
                  <Link
                    className='text-primary inline-flex items-center gap-1 underline underline-offset-4'
                    to='/support'
                  >
                    {t('Contact customer support')}
                    <ArrowUpRight className='size-3.5' />
                  </Link>
                </CardContent>
              </Card>
            </div>
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>

      {simpleCodeOpen && (
        <SimpleCodeForm
          balance={account}
          onClose={() => setSimpleCodeOpen(false)}
        />
      )}
      {simpleRechargeOpen && (
        <SimpleRechargeForm onClose={() => setSimpleRechargeOpen(false)} />
      )}
      {repaymentOpen && (
        <SimpleRechargeForm
          kind='repayment'
          amountDue={account.amount_due_quota}
          onClose={() => setRepaymentOpen(false)}
        />
      )}
      {incomeTransferOpen && (
        <SimpleIncomeTransferForm
          availableQuota={transferablePayment}
          onClose={() => setIncomeTransferOpen(false)}
        />
      )}
      {simplePaymentOpen && (
        <SimplePaymentForm onClose={() => setSimplePaymentOpen(false)} />
      )}
      {billsOpen && (
        <Dialog
          open
          title={t('Bills and reminders')}
          onOpenChange={setBillsOpen}
        >
          <SimpleHistory agentUserId={userId} initialKind='bills' />
        </Dialog>
      )}
    </>
  )
}

function Metric(props: {
  label: string
  value: ReactNode
  emphasis?: boolean
}) {
  return (
    <div>
      <dt className='text-xs text-sky-100/65'>{props.label}</dt>
      <dd
        className={
          props.emphasis
            ? 'mt-1 text-2xl font-semibold tabular-nums'
            : 'mt-1 text-lg font-medium tabular-nums'
        }
      >
        {props.value}
      </dd>
    </div>
  )
}

function RecentRedemption(props: { row: SimpleCodeRecord }) {
  const { t } = useTranslation()
  const status = isRedemptionExpired(props.row.expired_at, props.row.status)
    ? t('Expired')
    : t(REDEMPTION_STATUSES[props.row.status]?.labelKey ?? 'Unknown')
  return (
    <div className='grid grid-cols-[minmax(0,1fr)_auto] gap-3 border-b px-3 py-2.5 text-sm last:border-b-0'>
      <div>
        <p className='truncate font-medium'>{props.row.name}</p>
        <p className='text-muted-foreground text-xs'>
          {formatTimestampToDate(props.row.created_at)} ·{' '}
          {props.row.mode === 'payment'
            ? t('Customer payment')
            : t('Direct issue')}
        </p>
      </div>
      <div className='text-right'>
        <p className='font-medium tabular-nums'>
          {formatQuota(props.row.quota)}
        </p>
        <p className='text-muted-foreground text-xs'>{status}</p>
      </div>
    </div>
  )
}
