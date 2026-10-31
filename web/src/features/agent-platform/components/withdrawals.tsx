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

import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { handleServerError } from '@/lib/handle-server-error'
import { useAuthStore } from '@/stores/auth-store'

import { getPlatform } from '../api'
import { useRequestId } from '../hooks/use-request-id'
import type {
  CommissionWithdrawal,
  OperationDraft,
  OperationPayload,
} from '../types'
import { ActionForm, type ActionField } from './action-form'
import { OperationDialog } from './operation-dialog'
import { ResourceTable } from './resource-table'
import { BusinessDate, BusinessState, Money } from './values'

interface ManualWithdrawalInstruction {
  business_no: string
  currency: string
  currency_exponent: number
  amount_minor: string
  recipient_registry_id: string
  recipient_label: string
  recipient_digest: string
  manual_only: boolean
}

function WithdrawalReview(props: {
  id: number
  finance: boolean
  onClose: () => void
}) {
  const { t } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const requestId = useRequestId()
  const [instructions, setInstructions] =
    useState<ManualWithdrawalInstruction | null>(null)
  const [loadingInstructions, setLoadingInstructions] = useState(false)
  const [action, setAction] = useState<string | null>(null)
  const [draft, setDraft] = useState<OperationDraft | null>(null)
  const query = useQuery({
    queryKey: ['agent-platform', userId, 'withdrawal', props.id],
    queryFn: ({ signal }) =>
      getPlatform<CommissionWithdrawal>(
        `/withdrawals/${props.id}`,
        undefined,
        signal
      ),
  })
  if (draft) {
    return (
      <OperationDialog
        key={draft.request_id}
        draft={draft}
        onClose={() => setDraft(null)}
        onComplete={() => {
          requestId.reset()
          setDraft(null)
          void query.refetch()
        }}
      />
    )
  }
  const withdrawal = query.data
  const loadInstructions = async () => {
    if (loadingInstructions) return
    setLoadingInstructions(true)
    try {
      const result = await getPlatform<ManualWithdrawalInstruction>(
        `/finance/withdrawals/${props.id}/instructions`
      )
      if (
        !result.manual_only ||
        !result.recipient_registry_id ||
        !withdrawal ||
        result.business_no !== withdrawal.business_no ||
        result.currency !== withdrawal.currency ||
        result.currency_exponent !== withdrawal.currency_exponent ||
        result.amount_minor !== withdrawal.net_minor
      ) {
        throw new Error(
          t('Manual payment instructions do not match the approved withdrawal.')
        )
      }
      setInstructions(result)
    } catch (error) {
      handleServerError(error)
    } finally {
      setLoadingInstructions(false)
    }
  }
  if (instructions) {
    return (
      <Dialog
        open
        title={t('Manual payment verification instructions')}
        description={t(
          'This view does not send money. Do not enable or process withdrawals unless an approved access-controlled recipient registry is configured and its record has been independently verified.'
        )}
        onOpenChange={(open) => {
          if (!open) setInstructions(null)
        }}
      >
        <dl className='grid grid-cols-1 gap-4 text-sm'>
          <div>
            <dt>{t('Payment business number')}</dt>
            <dd className='flex items-center gap-2 font-mono break-all'>
              {instructions.business_no}
              <CopyButton
                value={instructions.business_no}
                aria-label={t('Copy payment business number')}
              />
            </dd>
          </div>
          <div>
            <dt>{t('Net payout')}</dt>
            <dd>
              <Money
                minor={instructions.amount_minor}
                currency={instructions.currency}
                exponent={instructions.currency_exponent}
              />
            </dd>
          </div>
          <div>
            <dt>{t('Recipient')}</dt>
            <dd>{instructions.recipient_label}</dd>
          </div>
          <div>
            <dt>{t('Controlled recipient registry ID')}</dt>
            <dd className='flex items-center gap-2 font-mono break-all'>
              {instructions.recipient_registry_id}
              <CopyButton
                value={instructions.recipient_registry_id}
                aria-label={t('Copy controlled registry ID')}
              />
            </dd>
          </div>
          <div>
            <dt>{t('Recipient verification digest')}</dt>
            <dd className='font-mono break-all'>
              {instructions.recipient_digest}
            </dd>
          </div>
        </dl>
        <Alert className='mt-4'>
          <AlertDescription>
            {t(
              'Manual processing only. Keep this business number for submission and reconciliation. An unknown payment must never be sent again.'
            )}
          </AlertDescription>
        </Alert>
      </Dialog>
    )
  }
  const actionLabels: Record<string, string> = {
    'commission.withdraw.approve': t('Approve withdrawal'),
    'commission.withdraw.reject': t('Reject withdrawal'),
    'commission.withdraw.submit': t('Record payment submission'),
    'commission.withdraw.resolve': t('Reconcile payment result'),
  }
  if (action && withdrawal) {
    const fields: ActionField[] = [
      { name: 'reason', label: 'Reason', kind: 'textarea' },
    ]
    if (
      action === 'commission.withdraw.submit' ||
      action === 'commission.withdraw.resolve'
    ) {
      fields.push({
        name: 'evidence_reference',
        label: 'Payment evidence reference',
        required: action === 'commission.withdraw.resolve',
      })
    }
    if (action === 'commission.withdraw.resolve') {
      fields.push(
        {
          name: 'status',
          label: 'Actual payment result',
          kind: 'select',
          options: [
            { value: 'succeeded', label: 'Confirmed successful' },
            { value: 'failed', label: 'Confirmed unpaid' },
            { value: 'unknown', label: 'Unknown; keep funds held' },
          ],
        },
        {
          name: 'failure_confirmed_unpaid',
          label:
            'I have verified that no payment occurred (failed result only)',
          kind: 'checkbox',
        },
        {
          name: 'payout_provider',
          label: 'Payout provider (successful result only)',
          required: false,
        },
        {
          name: 'payout_merchant_scope',
          label: 'Payout merchant scope (successful result only)',
          required: false,
        },
        {
          name: 'payout_environment',
          label: 'Payout environment (successful result only)',
          kind: 'select',
          required: false,
          options: [
            { value: 'live', label: 'Live' },
            { value: 'test', label: 'Test' },
          ],
        },
        {
          name: 'payout_reference',
          label: 'Canonical payout reference (successful result only)',
          required: false,
          description:
            'Use the actual provider payout identifier, not a local note. Stripe payouts use po_ or tr_. This receipt cannot settle a second payment.',
        }
      )
    }
    return (
      <ActionForm
        title={actionLabels[action]}
        description={`${t('Use this existing payment business number only')}: ${withdrawal.business_no}`}
        fields={fields}
        onClose={() => setAction(null)}
        onSubmit={(values) => {
          const payload: OperationPayload = {
            withdrawal_id: withdrawal.id,
            version: withdrawal.version,
            decision: '',
            status: '',
            reason: String(values.reason).trim(),
            evidence_reference: String(values.evidence_reference ?? '').trim(),
            failure_confirmed_unpaid: false,
          }
          let endpoint = 'approve'
          if (action === 'commission.withdraw.approve') {
            payload.decision = 'approve'
          }
          if (action === 'commission.withdraw.reject') {
            payload.decision = 'reject'
            endpoint = 'reject'
          }
          if (action === 'commission.withdraw.submit') endpoint = 'start'
          if (action === 'commission.withdraw.resolve') {
            endpoint = 'resolve'
            payload.status = String(values.status)
            payload.failure_confirmed_unpaid =
              values.failure_confirmed_unpaid === true
            for (const field of [
              'payout_provider',
              'payout_merchant_scope',
              'payout_environment',
              'payout_reference',
            ]) {
              const value = String(values[field] ?? '').trim()
              if ((payload.status === 'succeeded') !== Boolean(value)) {
                throw new Error(
                  t(
                    'A successful result requires the complete unique provider payout receipt. Other results must not include a payout receipt.'
                  )
                )
              }
              payload[field] = value
            }
            if (
              (payload.status === 'failed') !==
              payload.failure_confirmed_unpaid
            ) {
              throw new Error(
                t(
                  'A failed result requires verified evidence that no payment occurred.'
                )
              )
            }
          }
          setDraft({
            action,
            request_id: requestId({ action, ...payload }),
            payload,
            endpoint: `/finance/withdrawals/${withdrawal.id}/${endpoint}`,
            resource: '/finance/withdrawals',
            review: [
              {
                label: 'Payment business number',
                after: withdrawal.business_no,
              },
              { label: 'Operation', after: actionLabels[action] },
              ...fields.map((field) => ({
                label: field.label,
                after: String(values[field.name] ?? ''),
              })),
            ],
          })
          setAction(null)
        }}
      />
    )
  }
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={t('Withdrawal detail')}
      description={t(
        'Amounts and recipient versions are immutable. Changing either requires a new request and approval, never editing this payment.'
      )}
    >
      {query.isPending && <LoadingState />}
      {query.isError && (
        <ErrorState
          onRetry={() => {
            void query.refetch()
          }}
        />
      )}
      {withdrawal && (
        <div className='flex flex-col gap-4'>
          <dl className='grid grid-cols-1 gap-3 text-sm sm:grid-cols-2'>
            <div>
              <dt>{t('Payment business number')}</dt>
              <dd className='flex items-center gap-1 font-mono break-all'>
                {withdrawal.business_no}
                <CopyButton
                  value={withdrawal.business_no}
                  aria-label={t('Copy payment business number')}
                />
              </dd>
            </div>
            <div>
              <dt>{t('Status')}</dt>
              <dd>
                <BusinessState
                  value={
                    withdrawal.status === 'failed'
                      ? 'explicit_failed'
                      : withdrawal.status
                  }
                />
              </dd>
            </div>
            <div>
              <dt>{t('Original amount')}</dt>
              <dd>
                <Money
                  minor={withdrawal.amount_minor}
                  currency={withdrawal.currency}
                  exponent={withdrawal.currency_exponent}
                />
              </dd>
            </div>
            <div>
              <dt>{t('Fee')}</dt>
              <dd>
                <Money
                  minor={withdrawal.fee_minor}
                  currency={withdrawal.currency}
                  exponent={withdrawal.currency_exponent}
                />
              </dd>
            </div>
            <div>
              <dt>{t('Net payout')}</dt>
              <dd>
                <Money
                  minor={withdrawal.net_minor}
                  currency={withdrawal.currency}
                  exponent={withdrawal.currency_exponent}
                />
              </dd>
            </div>
            <div>
              <dt>{t('Recipient')}</dt>
              <dd>
                {withdrawal.recipient_label} · {withdrawal.recipient_masked} · v
                {withdrawal.recipient_version}
              </dd>
            </div>
            <div>
              <dt>{t('Requested by')}</dt>
              <dd>{withdrawal.requested_by}</dd>
            </div>
            <div>
              <dt>{t('Approved by')}</dt>
              <dd>{withdrawal.approved_by || '—'}</dd>
            </div>
            <div>
              <dt>{t('Payment evidence reference')}</dt>
              <dd className='break-all'>
                {withdrawal.evidence_reference || '—'}
              </dd>
            </div>
          </dl>
          {(withdrawal.status === 'unknown' ||
            withdrawal.status === 'paying') && (
            <Alert variant='destructive'>
              <AlertDescription>
                {t(
                  'This payment is still held. Do not initiate another payout. Record a confirmed outcome against the existing business number.'
                )}
              </AlertDescription>
            </Alert>
          )}
          {props.finance &&
            withdrawal.allowed_actions?.includes(
              'commission.withdraw.instructions'
            ) && (
              <Button
                variant='outline'
                disabled={loadingInstructions}
                onClick={() => {
                  void loadInstructions()
                }}
              >
                {t('View manual payment instructions')}
              </Button>
            )}
          {props.finance && (
            <div className='flex flex-wrap gap-2'>
              {withdrawal.allowed_actions
                ?.filter((key) => key in actionLabels)
                .map((key) => (
                  <Button
                    key={key}
                    variant='outline'
                    onClick={() => setAction(key)}
                  >
                    {actionLabels[key]}
                  </Button>
                ))}
            </div>
          )}
        </div>
      )}
    </Dialog>
  )
}

export function Withdrawals(props: { finance?: boolean }) {
  const userId = useAuthStore((state) => state.auth.user?.id)
  const { t } = useTranslation()
  const [selected, setSelected] = useState<number | null>(null)
  const columns: ColumnDef<CommissionWithdrawal>[] = [
    {
      accessorKey: 'business_no',
      header: t('Payment business number'),
      meta: { mobileTitle: true },
    },
    {
      accessorKey: 'status',
      header: t('Status'),
      meta: { mobileBadge: true },
      cell: ({ row }) => (
        <BusinessState
          value={
            row.original.status === 'failed'
              ? 'explicit_failed'
              : row.original.status
          }
        />
      ),
    },
    { accessorKey: 'user_id', header: t('User ID') },
    ...(['amount_minor', 'fee_minor', 'net_minor'] as const).map((key) => ({
      accessorKey: key,
      header: t(
        {
          amount_minor: 'Original amount',
          fee_minor: 'Fee',
          net_minor: 'Net payout',
        }[key]
      ),
      cell: ({ row }: { row: { original: CommissionWithdrawal } }) => (
        <Money
          minor={row.original[key]}
          currency={row.original.currency}
          exponent={row.original.currency_exponent}
        />
      ),
    })),
    { accessorKey: 'recipient_label', header: t('Recipient') },
    { accessorKey: 'recipient_version', header: t('Recipient version') },
    {
      accessorKey: 'created_at',
      header: t('Created'),
      cell: ({ row }) => <BusinessDate value={row.original.created_at} />,
    },
    {
      id: 'actions',
      header: t('Actions'),
      cell: ({ row }) => (
        <Button
          size='sm'
          variant='outline'
          onClick={() => setSelected(row.original.id)}
        >
          {t('View details')}
        </Button>
      ),
    },
  ]
  return (
    <div className='flex flex-col gap-4'>
      <Alert>
        <AlertDescription>
          {t(
            'Withdrawals reserve funds immediately. Only confirmed unpaid failures release the hold. Unknown results require reconciliation, not another payment.'
          )}
        </AlertDescription>
      </Alert>
      <ResourceTable
        resource={props.finance ? '/finance/withdrawals' : '/withdrawals'}
        columns={columns}
      />
      {selected !== null && (
        <WithdrawalReview
          key={`${userId}:${selected}`}
          id={selected}
          finance={props.finance ?? false}
          onClose={() => setSelected(null)}
        />
      )}
    </div>
  )
}
