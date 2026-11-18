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
import { useMutation } from '@tanstack/react-query'
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Field, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import {
  SecureVerificationDialog,
  useSecureVerification,
} from '@/features/auth/secure-verification'
import { toIntlLocale } from '@/i18n/languages'
import { hasPermission } from '@/lib/admin-permissions'
import { formatQuota } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { useAuthStore } from '@/stores/auth-store'

import { formatUSDT, newWithdrawalRequestKey } from './amount'
import { actOnWithdrawal } from './api'
import {
  withdrawalActions,
  withdrawalStatuses,
  withdrawalBadgeVariant,
  type Withdrawal,
  type WithdrawalAction,
} from './types'

const actionDescriptions: Record<WithdrawalAction['action'], string> = {
  approve:
    'Approve this request using its original rate, network and receiving address.',
  reject:
    'Reject this request and release the reserved rewards. Explain the reason to the user.',
  cancel: 'Cancel this pending request and release its reserved rewards.',
  begin_payment:
    'Begin manual payment processing. The request stays reserved and can no longer be cancelled or rejected.',
  paid: 'Record a completed manual payment. This deducts the reserved rewards once and saves the transaction hash.',
}

export function WithdrawalDetailDialog({
  row,
  admin,
  onClose,
  onChanged,
}: {
  row: Withdrawal
  admin: boolean
  onClose: () => void
  onChanged: () => Promise<void>
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.language)
  const user = useAuthStore((state) => state.auth.user)
  const [action, setAction] = useState<WithdrawalAction['action'] | null>(null)
  const [note, setNote] = useState('')
  const [hash, setHash] = useState('')
  const pendingKey = useRef<{ signature: string; key: string } | null>(null)
  const verification = useSecureVerification()
  const own = user?.id === row.user_id
  const canReview =
    admin && !own && hasPermission(user, 'referral_withdrawals', 'review')
  const canPay =
    admin && !own && hasPermission(user, 'referral_withdrawals', 'pay')
  const mutation = useMutation({
    mutationFn: async (input: WithdrawalAction) => {
      const signature = JSON.stringify([row.id, input])
      if (pendingKey.current?.signature !== signature) {
        pendingKey.current = { signature, key: newWithdrawalRequestKey() }
      }
      const key = pendingKey.current.key
      let proof: string | undefined
      if (input.action !== 'cancel') {
        const result = await verification.requestVerification({
          scope: 'referral.withdrawal.review',
          context: { withdrawal_id: row.id, request_key: key, input },
          title: t(withdrawalActions[input.action]),
          description: t(
            'Authorize {{amount}} USDT via {{network}} to {{address}}.',
            {
              amount: formatUSDT(row.net_usdt_micro),
              network: row.network,
              address: row.address,
            }
          ),
        })
        if (!result) return false
        proof = result.proof_token
      }
      await actOnWithdrawal(row.id, input, key, proof)
      return true
    },
    onSuccess: async (changed) => {
      if (changed) {
        setAction(null)
        setNote('')
        setHash('')
        await onChanged()
      }
    },
  })
  const choose = (next: WithdrawalAction['action']) => {
    setNote('')
    setHash('')
    setAction(next)
  }
  const validAction = action !== 'reject' || note.trim().length > 0
  const validHash =
    action !== 'paid' || /^(?:0x)?[0-9a-fA-F]{64}$/.test(hash.trim())
  const confirm = async () => {
    if (!action || !validAction || !validHash || mutation.isPending) return
    try {
      await mutation.mutateAsync({
        action,
        expected_revision: row.revision,
        note: note.trim(),
        transaction_hash: hash.trim(),
      })
    } catch (error) {
      handleServerError(error)
    }
  }
  return (
    <>
      <Dialog
        open={!action && !verification.isActive}
        onOpenChange={(open) => {
          if (!open && !mutation.isPending && !action) onClose()
        }}
        title={t('Withdrawal #{{id}}', { id: row.id })}
        description={t(
          'Review the payment destination, frozen rewards and processing history.'
        )}
        contentClassName='sm:max-w-2xl'
        footer={
          <>
            {!admin && row.status === 'pending' && (
              <Button variant='outline' onClick={() => choose('cancel')}>
                {t('Cancel withdrawal')}
              </Button>
            )}
            {canReview && ['pending', 'approved'].includes(row.status) && (
              <Button variant='destructive' onClick={() => choose('reject')}>
                {t('Reject withdrawal')}
              </Button>
            )}
            {canReview && row.status === 'pending' && (
              <Button onClick={() => choose('approve')}>
                {t('Approve withdrawal')}
              </Button>
            )}
            {canPay && row.status === 'approved' && (
              <Button onClick={() => choose('begin_payment')}>
                {t('Start manual payment')}
              </Button>
            )}
            {canPay && row.status === 'paying' && (
              <Button onClick={() => choose('paid')}>
                {t('Record completed payment')}
              </Button>
            )}
            <Button variant='outline' onClick={onClose}>
              {t('Close')}
            </Button>
          </>
        }
      >
        <div className='space-y-5'>
          <div className='flex flex-wrap items-center justify-between gap-3'>
            <Badge variant={withdrawalBadgeVariant(row.status)}>
              {t(withdrawalStatuses[row.status])}
            </Badge>
            <span className='text-muted-foreground text-xs'>
              {new Date(row.created_at * 1000).toLocaleString(locale)}
              {admin && ` · ${t('User #{{id}}', { id: row.user_id })}`}
            </span>
          </div>
          <div className='grid grid-cols-3 gap-3'>
            {[
              ['Withdrawal amount', row.gross_usdt_micro],
              ['Network fee', row.fee_usdt_micro],
              ['You receive', row.net_usdt_micro],
            ].map(([label, amount]) => (
              <div className='bg-muted/40 rounded-xl border p-3' key={label}>
                <p className='text-muted-foreground text-xs'>{t(label)}</p>
                <p className='mt-2 text-base font-semibold break-all tabular-nums'>
                  {formatUSDT(amount)}{' '}
                  <span className='text-muted-foreground text-xs'>USDT</span>
                </p>
              </div>
            ))}
          </div>
          <dl className='space-y-3 text-sm'>
            <div className='flex justify-between gap-4'>
              <dt className='text-muted-foreground'>{t('Network')}</dt>
              <dd className='font-semibold'>{row.network}</dd>
            </div>
            <div className='flex justify-between gap-4'>
              <dt className='text-muted-foreground'>{t('Rewards reserved')}</dt>
              <dd>{formatQuota(row.amount_quota)}</dd>
            </div>
            <div>
              <dt className='text-muted-foreground'>
                {t('Receiving address')}
              </dt>
              <dd className='mt-1 flex items-center gap-2 rounded-lg border p-2'>
                <span className='min-w-0 flex-1 font-mono text-xs break-all'>
                  {row.address}
                </span>
                <CopyButton
                  value={row.address}
                  aria-label={t('Copy receiving address')}
                />
              </dd>
            </div>
            {row.transaction_hash && (
              <div>
                <dt className='text-muted-foreground'>
                  {t('Transaction hash')}
                </dt>
                <dd className='mt-1 flex items-center gap-2 rounded-lg border p-2'>
                  <span className='min-w-0 flex-1 font-mono text-xs break-all'>
                    {row.transaction_hash}
                  </span>
                  <CopyButton
                    value={row.transaction_hash}
                    aria-label={t('Copy transaction hash')}
                  />
                </dd>
              </div>
            )}
          </dl>
          {row.status === 'paying' && (
            <Alert>
              <AlertDescription>
                {t(
                  'Manual payment is in progress. Rewards remain reserved until the administrator records the completed transaction.'
                )}
              </AlertDescription>
            </Alert>
          )}
          {row.review_note && (
            <Alert>
              <AlertDescription className='whitespace-pre-wrap'>
                {row.review_note}
              </AlertDescription>
            </Alert>
          )}
          <div>
            <h3 className='mb-3 font-medium'>{t('Processing history')}</h3>
            <ol className='space-y-4 border-l pl-4'>
              {row.events?.map((event) => (
                <li key={event.id} className='relative space-y-1'>
                  <span className='bg-primary absolute top-1.5 -left-[21px] size-2 rounded-full' />
                  <div className='flex flex-wrap items-center justify-between gap-2'>
                    <span className='text-sm'>
                      {event.action === 'created'
                        ? t('Request submitted')
                        : t(
                            withdrawalActions[
                              event.action as WithdrawalAction['action']
                            ] ?? event.action
                          )}
                    </span>
                    <time className='text-muted-foreground text-xs'>
                      {new Date(event.created_at * 1000).toLocaleString(locale)}
                    </time>
                  </div>
                  {event.note && (
                    <p className='text-muted-foreground text-xs whitespace-pre-wrap'>
                      {event.note}
                    </p>
                  )}
                  {event.amount_delta !== 0 && (
                    <p className='text-muted-foreground text-xs'>
                      {t('Rewards deducted')}:{' '}
                      {formatQuota(-event.amount_delta)}
                    </p>
                  )}
                </li>
              ))}
            </ol>
          </div>
        </div>
      </Dialog>
      {action && (
        <ConfirmDialog
          open={!verification.isActive}
          onOpenChange={(open) => {
            if (!open && !mutation.isPending) setAction(null)
          }}
          title={t(withdrawalActions[action])}
          desc={
            <div className='space-y-3'>
              <p>{t(actionDescriptions[action])}</p>
              <p className='font-medium'>
                {formatUSDT(row.net_usdt_micro)} USDT · {row.network}
              </p>
              <p className='font-mono text-xs break-all'>{row.address}</p>
            </div>
          }
          confirmText={t(withdrawalActions[action])}
          destructive={action === 'reject' || action === 'cancel'}
          isLoading={mutation.isPending}
          disabled={!validAction || !validHash}
          handleConfirm={() => void confirm()}
        >
          <div className='space-y-4'>
            {action === 'paid' && (
              <Field>
                <FieldLabel htmlFor='withdrawal-tx-hash'>
                  {t('Transaction hash')}
                </FieldLabel>
                <Input
                  id='withdrawal-tx-hash'
                  value={hash}
                  maxLength={66}
                  onChange={(e) => setHash(e.target.value)}
                  disabled={mutation.isPending}
                  autoComplete='off'
                  spellCheck={false}
                />
              </Field>
            )}
            {action !== 'cancel' && (
              <Field>
                <FieldLabel htmlFor='withdrawal-review-note'>
                  {t('Review note')}
                  {action === 'reject' ? ' *' : ''}
                </FieldLabel>
                <Textarea
                  id='withdrawal-review-note'
                  value={note}
                  onChange={(e) => setNote(e.target.value)}
                  maxLength={1000}
                  disabled={mutation.isPending}
                />
              </Field>
            )}
          </div>
        </ConfirmDialog>
      )}
      <SecureVerificationDialog {...verification.dialogProps} />
    </>
  )
}
