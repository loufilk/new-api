/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { formatTimestampToDate } from '@/lib/format'
import { translateServerText } from '@/lib/server-error-message'

import { formatAgentCash, formatAgentQuota } from '../lib/money'
import { getSimpleHistory } from '../simple-root-api'
import { SimpleRecordPager } from './simple-record-pager'

const kinds = {
  ledger: 'Fund history',
  recharges: 'Agent recharge records',
  bills: 'Weekly bills',
  reminders: 'Overdue reminders',
  withdrawals: 'Withdrawal records',
  exits: 'Exit settlements',
  referrals: 'Referral records',
} as const
type Kind = keyof typeof kinds

export function SimpleHistory(props: {
  agentUserId?: number
  initialKind?: Kind
  remindersOnly?: boolean
}) {
  const { t } = useTranslation()
  const [kind, setKind] = useState<Kind>(props.initialKind ?? 'ledger')
  const [page, setPage] = useState(1)
  const query = useQuery({
    queryKey: ['simple-root', 'history', props.agentUserId, kind, page],
    queryFn: () =>
      getSimpleHistory(kind, {
        agent_user_id: props.agentUserId,
        p: page,
        page_size: 20,
      }),
  })
  let content
  if (query.isPending) content = <LoadingState />
  else if (query.isError) {
    content = (
      <ErrorState
        onRetry={() => {
          void query.refetch()
        }}
      />
    )
  } else {
    content = (
      <>
        {query.data.items.length ? (
          <div className='divide-y rounded-lg border'>
            {query.data.items.map((row) => (
              <HistoryRow key={String(row.id)} row={row} kind={kind} />
            ))}
          </div>
        ) : (
          <EmptyState title={t('No history records')} />
        )}
        <SimpleRecordPager
          page={page}
          total={query.data.total}
          onChange={setPage}
        />
      </>
    )
  }
  return (
    <div className='space-y-3'>
      {!props.remindersOnly && (
        <NativeSelect
          className='w-48'
          aria-label={t('History type')}
          value={kind}
          onChange={(event) => {
            setKind(event.target.value as Kind)
            setPage(1)
          }}
        >
          {Object.entries(kinds).map(([value, label]) => (
            <NativeSelectOption key={value} value={value}>
              {t(label)}
            </NativeSelectOption>
          ))}
        </NativeSelect>
      )}
      {content}
    </div>
  )
}

function HistoryRow(props: {
  row: Record<string, string | number | boolean>
  kind: Kind
}) {
  const { t } = useTranslation()
  const row = props.row
  const number = (key: string) => Number(row[key] ?? 0)
  const details: [string, string][] = []
  const amount = (key: string, label: string, cash = false) => {
    if (number(key) !== 0) {
      details.push([
        t(label),
        cash ? formatAgentCash(number(key)) : formatAgentQuota(number(key)),
      ])
    }
  }
  if (props.kind === 'ledger') {
    for (const [key, label] of [
      ['delta_wallet_quota', 'Transferred wallet balance'],
      ['delta_cash_quota', 'Cash recharge'],
      ['delta_reward_quota', 'Referral reward ($)'],
      ['delta_root_quota', 'Root allocation'],
      ['delta_payment_quota', 'Transferred payment income'],
      ['delta_payment_available', 'Payment income available'],
      ['delta_payment_pending', 'Profit frozen'],
      ['delta_payment_debt', 'Recovery debt'],
    ]) {
      amount(key, label)
    }
    amount('delta_credit_outstanding', 'Amount due', true)
    amount('delta_credit_reserved', 'Reserved credit', true)
    amount('delta_credit_limit', 'Credit limit (CNY)', true)
  } else if (props.kind === 'bills') {
    amount('quota', 'Bill amount', true)
    amount('paid_quota', 'Repaid amount', true)
    details.push(
      [
        t('Amount due'),
        formatAgentCash(Math.max(0, number('quota') - number('paid_quota'))),
      ],
      [t('Due date'), formatTimestampToDate(number('due_at'))]
    )
  } else if (props.kind === 'recharges') {
    amount('agent_simple_quota', 'Quota')
    amount(
      'agent_simple_deferred_quota',
      'Paid credit awaiting balance capacity'
    )
    details.push(
      [t('Paid amount'), `¥${number('money').toFixed(2)}`],
      [t('Status'), t(String(row.status ?? ''))],
      [t('Payment method'), String(row.payment_method ?? '')]
    )
  } else if (props.kind === 'withdrawals') {
    amount('referral_quota', 'Referral reward ($)')
    amount('payment_quota', 'Payment-code profit ($)')
    amount('fee_quota', 'Fee')
    amount('net_quota', 'Net amount')
  } else if (props.kind === 'exits') {
    amount('quota', 'Quota')
    details.push(
      [
        t('Source'),
        t(
          {
            cash: 'Cash recharge',
            wallet: 'Transferred wallet balance',
            income: 'Transferred income',
          }[String(row.source)] ?? String(row.source)
        ),
      ],
      [t('Target'), t(row.target === 'refund' ? 'Cash refund' : 'User wallet')]
    )
    if (number('net_fen') > 0) {
      details.push([
        t('Refund amount'),
        `¥${(number('net_fen') / 100).toFixed(2)}`,
      ])
    }
  } else if (props.kind === 'referrals') {
    amount('paid_quota', 'Recharge Amount')
    amount('reward_quota', 'Referral reward ($)')
    amount('reversed_quota', 'Reversed amount')
    const status =
      {
        frozen: t('Reward pending'),
        released: t('Reward released'),
        cancelled: t('Reward cancelled'),
      }[String(row.status)] ?? String(row.status)
    details.push(
      [t('Status'), status],
      [t('Rate'), `${(number('rate') * 100).toFixed(2)}%`]
    )
    if (row.status === 'frozen') {
      details.push([
        t('Expected arrival'),
        formatTimestampToDate(number('matures_at')),
      ])
    }
  } else {
    amount('quota', 'Amount due', true)
    details.push([t('Reminder date'), String(row.day)])
  }
  const actor = number('actor_user_id')
  return (
    <article className='space-y-2 p-3 text-xs'>
      <div className='flex flex-wrap justify-between gap-2'>
        <span className='font-medium'>
          {t(kinds[props.kind])} #{String(row.id)}
        </span>
        <span className='text-muted-foreground'>
          {formatTimestampToDate(number('created_at') || number('create_time'))}
        </span>
      </div>
      <div className='text-muted-foreground'>
        {t('User ID')}: {String(row.agent_user_id ?? row.user_id ?? '')}
        {actor > 0 ? ` · ${t('Operator')}: ${actor}` : ''}
      </div>
      <dl className='grid grid-cols-2 gap-2 sm:grid-cols-3'>
        {details.map(([label, value]) => (
          <div key={label}>
            <dt className='text-muted-foreground'>{label}</dt>
            <dd className='font-medium break-words'>{value}</dd>
          </div>
        ))}
      </dl>
      {row.reason && (
        <p className='break-words'>
          {t('Reason')}: {translateServerText(t, String(row.reason))}
        </p>
      )}
      {row.evidence && (
        <p className='text-muted-foreground break-words'>
          {t('Support evidence')}: {String(row.evidence)}
        </p>
      )}
      {row.reference_id && (
        <p className='text-muted-foreground break-all'>
          {t('Reference')}: {String(row.reference_id)}
        </p>
      )}
    </article>
  )
}
