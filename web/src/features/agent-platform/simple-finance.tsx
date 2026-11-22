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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { useRequestId } from '@/features/agent-platform/hooks/use-request-id'

import { ActionForm, type ActionField } from './components/action-form'
import { SimpleHistory } from './components/simple-history'
import { SimplePaymentFeeForm } from './components/simple-payment-fee-form'
import { SimpleRecordPager } from './components/simple-record-pager'
import { SimpleRecords } from './components/simple-records'
import {
  agentRepaymentAmount,
  formatAgentCash,
  formatAgentQuota as formatQuota,
  parseAgentQuota,
} from './lib/money'
import {
  getSimpleAgents,
  adjustSimpleBalance,
  repaySimpleCredit,
  recordSimpleWithdrawal,
  setSimpleCreditLimit,
  settleSimpleExit,
  updateSimpleStatus,
  type SimpleAgentRow,
} from './simple-root-api'

const actionFields: Record<string, ActionField[]> = {
  adjust: [
    {
      name: 'delta',
      label: 'Balance adjustment ($)',
      kind: 'text',
      validate: (value) =>
        /^-?\d+(\.\d{1,2})?$/.test(String(value)) && Number(value) !== 0
          ? undefined
          : 'Enter a valid amount',
    },
    { name: 'reason', label: 'Reason', kind: 'textarea', max: 255 },
  ],
  credit: [
    {
      name: 'limit',
      label: 'Credit limit (CNY)',
      kind: 'text',
      validate: (value) =>
        (parseAgentQuota(String(value)) ?? -1) >= 0
          ? undefined
          : 'Enter a valid amount',
    },
    { name: 'reason', label: 'Reason', kind: 'textarea', max: 255 },
  ],
  repay: [
    {
      name: 'quota',
      label: 'Repayment amount (CNY)',
      kind: 'text',
      description:
        'Repay the debt in CNY, rounded up to the nearest fen. Any change is added to your agent balance.',
      validate: (value) =>
        (parseAgentQuota(String(value)) ?? 0) > 0
          ? undefined
          : 'Enter a valid amount',
    },
    { name: 'reason', label: 'Reason', kind: 'textarea', max: 255 },
  ],
  withdrawal: [
    {
      name: 'referral',
      label: 'Referral reward ($)',
      kind: 'text',
      validate: (value) =>
        (parseAgentQuota(String(value)) ?? -1) >= 0
          ? undefined
          : 'Enter a valid amount',
    },
    {
      name: 'payment',
      label: 'Payment-code profit ($)',
      kind: 'text',
      validate: (value) =>
        (parseAgentQuota(String(value)) ?? -1) >= 0
          ? undefined
          : 'Enter a valid amount',
    },
    { name: 'reason', label: 'Reason', kind: 'textarea', max: 255 },
    { name: 'evidence', label: 'Support evidence', kind: 'textarea', max: 255 },
  ],
  exit: [
    {
      name: 'quota',
      label: 'Amount ($)',
      kind: 'text',
      required: false,
      validate: (value) =>
        !String(value).trim() || (parseAgentQuota(String(value)) ?? 0) > 0
          ? undefined
          : 'Enter a valid amount',
    },
    {
      name: 'settle_all',
      label: 'Settle all available balance',
      kind: 'checkbox',
    },
    {
      name: 'source',
      label: 'Source',
      kind: 'select',
      options: [
        { value: 'cash', label: 'Cash recharge' },
        { value: 'income', label: 'Transferred income' },
        {
          value: 'wallet',
          label: 'Transferred wallet balance',
          updates: { target: 'wallet' },
        },
      ],
    },
    {
      name: 'target',
      label: 'Target',
      kind: 'select',
      description:
        'Transferred wallet balance can only return to the original wallet.',
      validate: (value, values) =>
        values.source === 'wallet' && value !== 'wallet'
          ? 'Transferred wallet balance can only return to the original wallet.'
          : undefined,
      options: [
        { value: 'wallet', label: 'User wallet' },
        { value: 'refund', label: 'Cash refund' },
      ],
    },
    { name: 'reason', label: 'Reason', kind: 'textarea', max: 255 },
    { name: 'evidence', label: 'Support evidence', kind: 'textarea', max: 255 },
  ],
}

export function SimpleAgentFinance() {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [page, setPage] = useState(1)
  const query = useQuery({
    queryKey: ['simple-root', 'agents', page],
    queryFn: () => getSimpleAgents({ p: page, page_size: 20 }),
  })
  const [selected, setSelected] = useState<SimpleAgentRow | null>(null)
  const [action, setAction] = useState<string | null>(null)
  const [recordsAgent, setRecordsAgent] = useState<number | undefined>()
  const [openAgent, setOpenAgent] = useState(false)
  const [feesOpen, setFeesOpen] = useState(false)
  const [userWithdrawalOpen, setUserWithdrawalOpen] = useState(false)
  const [detailsAgent, setDetailsAgent] = useState<SimpleAgentRow | null>(null)
  const [historyOpen, setHistoryOpen] = useState(false)
  const refresh = async () => {
    await client.invalidateQueries({ queryKey: ['simple-root'] })
    await client.invalidateQueries({ queryKey: ['agent-platform'] })
  }

  if (query.isPending) return <LoadingState />
  if (query.isError) return <ErrorState onRetry={() => void query.refetch()} />
  const agents = query.data?.items ?? []
  return (
    <>
      <SectionPageLayout stackActionsOnMobile>
        <SectionPageLayout.Title>
          {t('Agent management')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <div
            role='group'
            aria-label={t('Agent management')}
            className='flex max-w-full flex-wrap items-center gap-2'
          >
            <Button
              className='max-sm:flex-auto'
              onClick={() => setOpenAgent(true)}
            >
              {t('Open agent')}
            </Button>
            <Button
              className='max-sm:flex-auto'
              variant='outline'
              onClick={() => setFeesOpen(true)}
            >
              {t('Payment channel fees')}
            </Button>
            <Button
              className='max-sm:flex-auto'
              variant='outline'
              onClick={() => setUserWithdrawalOpen(true)}
            >
              {t('Record user withdrawal')}
            </Button>
            <Button
              className='max-sm:flex-auto'
              variant='outline'
              onClick={() => setHistoryOpen(true)}
            >
              {t('All financial history')}
            </Button>
          </div>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <div className='flex flex-col gap-4'>
            <Card>
              <CardHeader>
                <CardTitle>{t('Agents')}</CardTitle>
              </CardHeader>
              <CardContent className='flex flex-col gap-3'>
                {!agents.length ? (
                  <EmptyState
                    title={t('No agents found')}
                    className='min-h-48 sm:min-h-60'
                  />
                ) : (
                  agents.map((agent) => (
                    <div
                      key={agent.agent_user_id}
                      className='flex flex-wrap items-center gap-3 rounded-lg border p-3'
                    >
                      <div className='min-w-44 flex-1'>
                        <div className='font-medium'>{agent.username}</div>
                        <div className='text-muted-foreground text-xs'>
                          {{
                            active: t('Active'),
                            paused: t('Paused'),
                            cancelled: t('Cancelled'),
                          }[agent.status] ?? agent.status}
                          {agent.inactive ? ` · ${t('Inactive')}` : ''}
                        </div>
                      </div>
                      <div className='text-xs'>
                        <div>
                          {t('Agent balance')}:{' '}
                          {formatQuota(agent.available_quota)}
                        </div>
                        <div>
                          {t('Available credit')}:{' '}
                          {formatAgentCash(agent.available_credit_quota)}
                        </div>
                        <div>
                          {t('Amount due')}:{' '}
                          {formatAgentCash(agent.amount_due_quota)}
                        </div>
                        {agent.overdue_quota > 0 && (
                          <p role='status' className='text-destructive'>
                            {t(
                              'Overdue amount: {{amount}}. Please repay or contact support.',
                              { amount: formatAgentCash(agent.overdue_quota) }
                            )}
                          </p>
                        )}
                      </div>
                      <div className='flex flex-wrap gap-2'>
                        <Button
                          size='sm'
                          variant='outline'
                          onClick={() => setDetailsAgent(agent)}
                        >
                          {t('Fund details')}
                        </Button>
                        <Button
                          size='sm'
                          variant='outline'
                          onClick={() => {
                            setSelected(agent)
                            setAction(
                              agent.status === 'active' ? 'pause' : 'resume'
                            )
                          }}
                        >
                          {agent.status === 'active' ? t('Pause') : t('Resume')}
                        </Button>
                        <Button
                          size='sm'
                          variant='outline'
                          onClick={() => {
                            setSelected(agent)
                            setAction('adjust')
                          }}
                        >
                          {t('Adjust')}
                        </Button>
                        <Button
                          size='sm'
                          variant='outline'
                          onClick={() => {
                            setSelected(agent)
                            setAction('credit')
                          }}
                        >
                          {t('Credit limit')}
                        </Button>
                        <Button
                          size='sm'
                          variant='outline'
                          onClick={() => {
                            setSelected(agent)
                            setAction('repay')
                          }}
                        >
                          {t('Repayment')}
                        </Button>
                        <Button
                          size='sm'
                          variant='outline'
                          onClick={() => setRecordsAgent(agent.agent_user_id)}
                        >
                          {t('Codes')}
                        </Button>
                        <Button
                          size='sm'
                          variant='outline'
                          onClick={() => {
                            setSelected(agent)
                            setAction('withdrawal')
                          }}
                        >
                          {t('Record withdrawal')}
                        </Button>
                        {agent.status === 'paused' && (
                          <Button
                            size='sm'
                            variant='outline'
                            onClick={() => {
                              setSelected(agent)
                              setAction('cancel')
                            }}
                          >
                            {t('Cancel agent')}
                          </Button>
                        )}
                        {agent.status !== 'active' && (
                          <Button
                            size='sm'
                            variant='outline'
                            onClick={() => {
                              setSelected(agent)
                              setAction('exit')
                            }}
                          >
                            {t('Settle remaining balance')}
                          </Button>
                        )}
                      </div>
                    </div>
                  ))
                )}
                {(query.data.total > 0 || page > 1) && (
                  <SimpleRecordPager
                    page={page}
                    total={query.data.total}
                    onChange={setPage}
                  />
                )}
              </CardContent>
            </Card>
            {recordsAgent ? (
              <Card>
                <CardHeader>
                  <CardTitle>
                    {t('Redemption records')} ·{' '}
                    {
                      agents.find((a) => a.agent_user_id === recordsAgent)
                        ?.username
                    }
                  </CardTitle>
                </CardHeader>
                <CardContent>
                  <SimpleRecords
                    key={recordsAgent}
                    agentUserId={recordsAgent}
                  />
                </CardContent>
              </Card>
            ) : null}
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>
      {openAgent && (
        <OpenAgentForm onClose={() => setOpenAgent(false)} onDone={refresh} />
      )}
      {feesOpen && <SimplePaymentFeeForm onClose={() => setFeesOpen(false)} />}
      {historyOpen && (
        <Dialog
          open
          title={t('All financial history')}
          onOpenChange={(open) => {
            if (!open) setHistoryOpen(false)
          }}
        >
          <SimpleHistory />
        </Dialog>
      )}
      {detailsAgent && (
        <Dialog
          open
          title={`${t('Fund details')} · ${detailsAgent.username}`}
          onOpenChange={(open) => {
            if (!open) setDetailsAgent(null)
          }}
        >
          <dl className='mb-4 grid grid-cols-2 gap-3 text-sm'>
            {[
              [t('Cash recharge'), formatQuota(detailsAgent.cash_quota)],
              [
                t('Transferred wallet balance'),
                formatQuota(detailsAgent.wallet_quota ?? 0),
              ],
              [
                t('Referral reward ($)'),
                formatQuota(detailsAgent.reward_quota),
              ],
              [
                t('Transferred payment income'),
                formatQuota(detailsAgent.payment_quota),
              ],
              [t('Root allocation'), formatQuota(detailsAgent.root_quota)],
              [
                t('Credit limit (CNY)'),
                formatAgentCash(detailsAgent.credit_limit_quota),
              ],
            ].map(([label, value]) => (
              <div key={label}>
                <dt className='text-muted-foreground'>{label}</dt>
                <dd className='font-medium'>{value}</dd>
              </div>
            ))}
          </dl>
          <SimpleHistory
            key={detailsAgent.agent_user_id}
            agentUserId={detailsAgent.agent_user_id}
          />
        </Dialog>
      )}
      {userWithdrawalOpen && (
        <RootActionForm
          agent={{ agent_user_id: 0 }}
          action='withdrawal'
          selectUser
          onClose={() => setUserWithdrawalOpen(false)}
          onDone={refresh}
        />
      )}
      {selected &&
      action &&
      action !== 'pause' &&
      action !== 'resume' &&
      action !== 'cancel' ? (
        <RootActionForm
          agent={selected}
          action={action}
          onClose={() => {
            setAction(null)
            setSelected(null)
          }}
          onDone={refresh}
        />
      ) : null}
      {selected &&
      (action === 'pause' || action === 'resume' || action === 'cancel') ? (
        <RootStatusForm
          agent={selected}
          status={
            { pause: 'paused', resume: 'active', cancel: 'cancelled' }[action]
          }
          onClose={() => {
            setAction(null)
            setSelected(null)
          }}
          onDone={refresh}
        />
      ) : null}
    </>
  )
}

function RootStatusForm(props: {
  agent: SimpleAgentRow
  status: string
  onClose: () => void
  onDone: () => Promise<void>
}) {
  const { t } = useTranslation()
  const requestId = useRequestId()
  return (
    <ActionForm
      title={t('Change agent status')}
      fields={[
        { name: 'reason', label: 'Reason', kind: 'textarea', max: 255 },
        {
          name: 'confirmed',
          label: 'I confirm this change and its financial impact',
          kind: 'checkbox',
          mustBeTrue: true,
        },
      ]}
      onClose={props.onClose}
      onSubmit={async (values) => {
        await updateSimpleStatus({
          request_id: requestId(values),
          agent_user_id: props.agent.agent_user_id,
          status: props.status,
          reason: String(values.reason),
        })
        await props.onDone()
        props.onClose()
      }}
      submitLabel={t('Save')}
    />
  )
}

function RootActionForm(props: {
  agent: Pick<SimpleAgentRow, 'agent_user_id'> &
    Partial<Pick<SimpleAgentRow, 'amount_due_quota'>>
  action: string
  selectUser?: boolean
  onClose: () => void
  onDone: () => Promise<void>
}) {
  const { t } = useTranslation()
  const requestId = useRequestId()
  const initial: Record<string, string> = {}
  if (props.action === 'exit') {
    initial.source = 'cash'
    initial.target = 'wallet'
  } else if (props.action === 'repay') {
    initial.quota = agentRepaymentAmount(props.agent.amount_due_quota ?? 0)
  }
  const fields = [
    ...(props.selectUser
      ? [
          {
            name: 'user_id',
            label: 'User ID',
            kind: 'integer' as const,
            min: 1,
          },
        ]
      : []),
    ...actionFields[props.action],
    {
      name: 'confirmed',
      label:
        props.action === 'withdrawal'
          ? 'I confirm the manual payment is complete. Record it and deduct the balance.'
          : 'I confirm this change and its financial impact',
      kind: 'checkbox' as const,
      mustBeTrue: true,
    },
  ]
  return (
    <ActionForm
      title={t('Record agent finance action')}
      fields={fields}
      initial={initial}
      onClose={props.onClose}
      onSubmit={async (values) => {
        const id = requestId(values)
        const targetUserId = props.selectUser
          ? Number(values.user_id)
          : props.agent.agent_user_id
        const quota = (name: string) =>
          parseAgentQuota(String(values[name] ?? '0')) ?? Number.NaN
        if (props.action === 'adjust') {
          await adjustSimpleBalance({
            request_id: id,
            agent_user_id: props.agent.agent_user_id,
            delta: quota('delta'),
            reason: String(values.reason),
          })
        }
        if (props.action === 'credit') {
          await setSimpleCreditLimit({
            request_id: id,
            agent_user_id: props.agent.agent_user_id,
            limit_quota: quota('limit'),
            reason: String(values.reason),
          })
        }
        if (props.action === 'repay') {
          await repaySimpleCredit({
            request_id: id,
            agent_user_id: props.agent.agent_user_id,
            quota: quota('quota'),
            reason: String(values.reason),
          })
        }
        if (props.action === 'withdrawal') {
          await recordSimpleWithdrawal({
            request_id: id,
            user_id: targetUserId,
            referral_quota: quota('referral'),
            payment_quota: quota('payment'),
            reason: String(values.reason),
            evidence: String(values.evidence),
          })
        }
        if (props.action === 'exit') {
          const settleAll = values.settle_all === true
          await settleSimpleExit({
            request_id: id,
            agent_user_id: props.agent.agent_user_id,
            quota: settleAll ? 0 : quota('quota'),
            settle_all: settleAll,
            source: String(values.source),
            target: String(values.target),
            reason: String(values.reason),
            evidence: String(values.evidence),
          })
        }
        await props.onDone()
        props.onClose()
      }}
      submitLabel={t('Save')}
    />
  )
}

function OpenAgentForm(props: {
  onClose: () => void
  onDone: () => Promise<void>
}) {
  const { t } = useTranslation()
  const requestId = useRequestId()
  return (
    <ActionForm
      title={t('Open agent')}
      fields={[
        { name: 'user_id', label: 'User ID', kind: 'integer', min: 1 },
        { name: 'reason', label: 'Reason', kind: 'textarea', max: 255 },
        {
          name: 'confirmed',
          label: 'I confirm this change and its financial impact',
          kind: 'checkbox',
          mustBeTrue: true,
        },
      ]}
      onClose={props.onClose}
      onSubmit={async (values) => {
        await updateSimpleStatus({
          request_id: requestId(values),
          agent_user_id: Number(values.user_id),
          status: 'active',
          reason: String(values.reason),
        })
        await props.onDone()
        props.onClose()
      }}
      submitLabel={t('Save')}
    />
  )
}
