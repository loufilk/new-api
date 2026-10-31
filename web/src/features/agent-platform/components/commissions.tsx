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
import type { ColumnDef } from '@tanstack/react-table'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useAuthStore } from '@/stores/auth-store'

import { getPlatform } from '../api'
import type {
  CommissionSummary,
  CommissionAward,
  CommissionLedger,
  CommissionConversion,
  CommissionDebt,
  OperationDraft,
} from '../types'
import { CommissionRecipients } from './commission-recipients'
import { CommissionTransfer } from './commission-transfer'
import { OperationDialog } from './operation-dialog'
import { ResourceTable } from './resource-table'
import { BusinessDate, Money, Quota } from './values'

export function Commissions() {
  const { t } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const [transfer, setTransfer] = useState<
    'withdrawal' | 'wallet' | 'procurement' | null
  >(null)
  const [draft, setDraft] = useState<OperationDraft | null>(null)
  const query = useQuery({
    queryKey: ['agent-platform', userId, 'commission-summary'],
    queryFn: ({ signal }) =>
      getPlatform<CommissionSummary>('/commissions/summary', undefined, signal),
  })
  const awardColumns: ColumnDef<CommissionAward>[] = [
    {
      accessorKey: 'reference_id',
      header: t('Original payment'),
      meta: { mobileTitle: true },
    },
    { accessorKey: 'source_user_id', header: t('Invited user ID') },
    ...(
      ['paid_minor', 'reward_minor', 'reversed_minor', 'cap_minor'] as const
    ).map((key) => ({
      accessorKey: key,
      header: t(
        {
          paid_minor: 'Actual payment',
          reward_minor: 'Commission earned',
          reversed_minor: 'Commission reversed',
          cap_minor: 'Policy cap',
        }[key]
      ),
      cell: ({ row }: { row: { original: CommissionAward } }) => (
        <Money
          minor={row.original[key]}
          currency={row.original.currency}
          exponent={row.original.currency_exponent}
        />
      ),
    })),
    {
      accessorKey: 'matures_at',
      header: t('Maturity date'),
      cell: ({ row }) => <BusinessDate value={row.original.matures_at} />,
    },
    { accessorKey: 'policy_version', header: t('Policy version') },
  ]
  const ledgerColumns: ColumnDef<CommissionLedger>[] = [
    { accessorKey: 'id', header: t('Ledger ID'), meta: { mobileTitle: true } },
    { accessorKey: 'kind', header: t('Type') },
    { accessorKey: 'reference_id', header: t('Reference') },
    ...(
      [
        'delta_pending_minor',
        'delta_available_minor',
        'delta_reserved_minor',
        'delta_held_minor',
        'delta_debt_minor',
        'after_available_minor',
      ] as const
    ).map((key) => ({
      accessorKey: key,
      header: t(
        {
          delta_pending_minor: 'Pending change',
          delta_available_minor: 'Available change',
          delta_reserved_minor: 'Reserve change',
          delta_held_minor: 'Withdrawal hold change',
          delta_debt_minor: 'Debt change',
          after_available_minor: 'Available after operation',
        }[key]
      ),
      cell: ({ row }: { row: { original: CommissionLedger } }) => (
        <Money
          minor={row.original[key]}
          currency={row.original.currency}
          exponent={row.original.currency_exponent}
        />
      ),
    })),
    {
      accessorKey: 'created_at',
      header: t('Created'),
      cell: ({ row }) => <BusinessDate value={row.original.created_at} />,
    },
  ]
  const conversionColumns: ColumnDef<CommissionConversion>[] = [
    {
      accessorKey: 'id',
      header: t('Conversion ID'),
      meta: { mobileTitle: true },
    },
    { accessorKey: 'target', header: t('Destination') },
    {
      accessorKey: 'amount_minor',
      header: t('Original amount'),
      cell: ({ row }) => (
        <Money
          minor={row.original.amount_minor}
          currency={row.original.currency}
          exponent={row.original.currency_exponent}
        />
      ),
    },
    {
      accessorKey: 'quota',
      header: t('Received quota'),
      cell: ({ row }) => <Quota value={row.original.quota} />,
    },
    { accessorKey: 'policy_version', header: t('Policy version') },
  ]
  const debtColumns: ColumnDef<CommissionDebt>[] = [
    { accessorKey: 'id', header: t('Debt ID'), meta: { mobileTitle: true } },
    { accessorKey: 'award_id', header: t('Original commission') },
    { accessorKey: 'currency', header: t('Original currency') },
    { accessorKey: 'amount_minor', header: t('Recovery debt (minor units)') },
    {
      accessorKey: 'recovered_minor',
      header: t('Recovered amount (minor units)'),
    },
  ]
  if (query.isPending) return <LoadingState />
  if (query.isError || !query.data) {
    return (
      <ErrorState
        onRetry={() => {
          void query.refetch()
        }}
      />
    )
  }
  const summary = query.data
  return (
    <div className='flex flex-col gap-5'>
      <Alert>
        <AlertDescription>
          {t(
            'Cash commission is separate from your consumption wallet and legacy affiliate benefits. Maturity does not remove refund or chargeback risk. Reversals after payout or conversion create recovery debt.'
          )}
        </AlertDescription>
      </Alert>
      {summary.disabled_reason && (
        <p role='status' className='text-muted-foreground text-sm'>
          {t(summary.disabled_reason)}
        </p>
      )}
      {summary.accounts.map((account) => (
        <Card key={account.currency}>
          <CardHeader>
            <CardTitle>{account.currency}</CardTitle>
          </CardHeader>
          <CardContent>
            <dl className='grid grid-cols-2 gap-4 lg:grid-cols-5'>
              {(
                [
                  'pending_minor',
                  'available_minor',
                  'reserved_minor',
                  'withdrawal_held_minor',
                  'debt_minor',
                ] as const
              ).map((key) => (
                <div key={key}>
                  <dt className='text-muted-foreground text-sm'>
                    {t(
                      {
                        pending_minor: 'Pending maturity',
                        available_minor: 'Available commission',
                        reserved_minor: 'Risk reserve',
                        withdrawal_held_minor: 'Withdrawal holds',
                        debt_minor: 'Recovery debt',
                      }[key]
                    )}
                  </dt>
                  <dd>
                    <Money
                      minor={account[key]}
                      currency={account.currency}
                      exponent={account.currency_exponent}
                    />
                  </dd>
                </div>
              ))}
            </dl>
          </CardContent>
        </Card>
      ))}
      {!summary.accounts.length && (
        <p>
          {t(
            'No new cash commission accounts. Historical consumption benefits are unchanged.'
          )}
        </p>
      )}
      <div className='flex flex-wrap gap-2'>
        <Button
          disabled={
            !summary.allowed_actions.includes('commission.withdraw.request')
          }
          onClick={() => setTransfer('withdrawal')}
        >
          {t('Request withdrawal')}
        </Button>
        <Button
          variant='outline'
          disabled={
            !summary.allowed_actions.includes('commission.convert.wallet')
          }
          onClick={() => setTransfer('wallet')}
        >
          {t('Convert to reward wallet')}
        </Button>
        <Button
          variant='outline'
          disabled={
            !summary.allowed_actions.includes('commission.convert.procurement')
          }
          onClick={() => setTransfer('procurement')}
        >
          {t('Convert to procurement quota')}
        </Button>
      </div>
      <p className='text-muted-foreground text-sm'>
        {t('Legacy affiliate consumption benefit')}:{' '}
        <Quota value={summary.legacy_aff_quota} /> ·{' '}
        {t('Legacy benefits do not automatically become cashable.')}
      </p>
      <Tabs defaultValue='awards'>
        <TabsList
          className='h-auto flex-wrap'
          aria-label={t('Commission records')}
        >
          <TabsTrigger value='awards'>{t('Commission awards')}</TabsTrigger>
          <TabsTrigger value='ledger'>{t('Commission ledger')}</TabsTrigger>
          <TabsTrigger value='conversions'>{t('Conversions')}</TabsTrigger>
          <TabsTrigger value='debts'>{t('Recovery debt')}</TabsTrigger>
          <TabsTrigger value='recipients'>{t('Payout recipients')}</TabsTrigger>
        </TabsList>
        <TabsContent value='awards'>
          <ResourceTable
            resource='/commissions/awards'
            columns={awardColumns}
          />
        </TabsContent>
        <TabsContent value='ledger'>
          <ResourceTable
            resource='/commissions/ledger'
            columns={ledgerColumns}
          />
        </TabsContent>
        <TabsContent value='conversions'>
          <ResourceTable
            resource='/commissions/conversions'
            columns={conversionColumns}
          />
        </TabsContent>
        <TabsContent value='debts'>
          <ResourceTable resource='/commissions/debts' columns={debtColumns} />
        </TabsContent>
        <TabsContent value='recipients'>
          <CommissionRecipients
            enabled={summary.allowed_actions.includes('recipient.change')}
          />
        </TabsContent>
      </Tabs>
      {transfer && (
        <CommissionTransfer
          target={transfer}
          accounts={summary.accounts}
          onClose={() => setTransfer(null)}
          onPrepared={(operation) => {
            setTransfer(null)
            setDraft(operation)
          }}
        />
      )}
      {draft && (
        <OperationDialog
          key={draft.request_id}
          draft={draft}
          onClose={() => setDraft(null)}
          onComplete={() => setDraft(null)}
        />
      )}
    </div>
  )
}
