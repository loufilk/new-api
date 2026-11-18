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
import { ArrowRight, ShieldCheck } from 'lucide-react'
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Field, FieldError, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import {
  SecureVerificationDialog,
  useSecureVerification,
} from '@/features/auth/secure-verification'
import { formatQuota } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'

import {
  formatUSDT,
  newWithdrawalRequestKey,
  quotaToUSDT,
  usdtToQuota,
} from './amount'
import { createWithdrawal, quoteWithdrawal } from './api'
import type {
  Withdrawal,
  WithdrawalInput,
  WithdrawalNetwork,
  WithdrawalSummary,
} from './types'

export function CreateWithdrawalDialog({
  summary,
  onClose,
  onCreated,
}: {
  summary: WithdrawalSummary
  onClose: () => void
  onCreated: (row: Withdrawal) => Promise<void>
}) {
  const { t } = useTranslation()
  const [amount, setAmount] = useState(
    quotaToUSDT(
      summary.config.min_withdrawal_quota,
      summary.config.quota_per_usdt
    )
  )
  const [network, setNetwork] = useState<WithdrawalNetwork>('BSC')
  const [address, setAddress] = useState('')
  const [quote, setQuote] = useState<Withdrawal | null>(null)
  const [submitted, setSubmitted] = useState(false)
  const key = useRef<{ signature: string; value: string } | null>(null)
  const verification = useSecureVerification()
  const amountQuota = usdtToQuota(amount, summary.config.quota_per_usdt)
  const validAmount =
    amountQuota !== null &&
    amountQuota >= summary.config.min_withdrawal_quota &&
    amountQuota <= summary.available_quota
  const preview = useMutation({
    mutationFn: quoteWithdrawal,
    onSuccess: setQuote,
  })
  const submit = useMutation({
    mutationFn: async (input: WithdrawalInput) => {
      if (!quote) return null
      const signature = JSON.stringify(input)
      if (key.current?.signature !== signature) {
        key.current = { signature, value: newWithdrawalRequestKey() }
      }
      const requestKey = key.current.value
      const proof = await verification.requestVerification({
        scope: 'referral.withdrawal.create',
        context: { request_key: requestKey, input },
        title: t('Confirm withdrawal'),
        description: t(
          'Authorize {{amount}} USDT via {{network}} to {{address}}.',
          {
            amount: formatUSDT(quote.net_usdt_micro),
            network: input.network,
            address: input.address,
          }
        ),
      })
      if (!proof) return null
      return createWithdrawal(input, requestKey, proof.proof_token)
    },
    onSuccess: async (row) => {
      if (row) await onCreated(row)
    },
  })
  const busy = preview.isPending || submit.isPending
  const confirmLabel = quote ? t('Confirm withdrawal') : t('Review withdrawal')
  const checkQuote = async () => {
    setSubmitted(true)
    if (!validAmount || !address.trim()) return
    await preview
      .mutateAsync({
        amount_quota: amountQuota,
        network,
        address: address.trim(),
        policy_revision: summary.config.revision,
      })
      .catch(() => undefined)
  }
  const confirm = async () => {
    if (!quote || submit.isPending) return
    try {
      await submit.mutateAsync({
        amount_quota: quote.amount_quota,
        network: quote.network,
        address: quote.address,
        policy_revision: quote.policy_revision,
      })
    } catch (error) {
      handleServerError(error)
    }
  }
  return (
    <>
      <Dialog
        open={!verification.isActive}
        onOpenChange={(open) => {
          if (!open && !busy) onClose()
        }}
        title={t('Request a withdrawal')}
        description={t(
          'Choose a network and review the exact amount before confirming.'
        )}
        contentClassName='sm:max-w-xl'
        footer={
          <>
            <Button
              variant='outline'
              disabled={busy}
              onClick={quote ? () => setQuote(null) : onClose}
            >
              {quote ? t('Back') : t('Cancel')}
            </Button>
            <Button
              disabled={busy || !summary.config.enabled}
              onClick={() => void (quote ? confirm() : checkQuote())}
            >
              {busy ? t('Processing...') : confirmLabel}
              <ArrowRight aria-hidden />
            </Button>
          </>
        }
      >
        {quote ? (
          <div className='space-y-5'>
            <div className='bg-primary/5 rounded-xl border p-5'>
              <p className='text-muted-foreground text-sm'>
                {t('You receive')}
              </p>
              <p className='mt-2 text-3xl font-semibold tabular-nums'>
                {formatUSDT(quote.net_usdt_micro)}{' '}
                <span className='text-muted-foreground text-base'>USDT</span>
              </p>
            </div>
            <dl className='grid grid-cols-[auto_1fr] gap-x-5 gap-y-3 text-sm'>
              <dt className='text-muted-foreground'>
                {t('Withdrawal amount')}
              </dt>
              <dd className='text-right'>
                {formatUSDT(quote.gross_usdt_micro)} USDT
              </dd>
              <dt className='text-muted-foreground'>{t('Network fee')}</dt>
              <dd className='text-right'>
                {formatUSDT(quote.fee_usdt_micro)} USDT ({quote.fee_bps / 100}%)
              </dd>
              <dt className='text-muted-foreground'>{t('Rewards reserved')}</dt>
              <dd className='text-right'>{formatQuota(quote.amount_quota)}</dd>
              <dt className='text-muted-foreground'>{t('Network')}</dt>
              <dd className='text-right font-medium'>{quote.network}</dd>
              <dt className='text-muted-foreground'>
                {t('Receiving address')}
              </dt>
              <dd className='text-right font-mono text-xs break-all'>
                {quote.address}
              </dd>
            </dl>
            <Alert>
              <ShieldCheck aria-hidden />
              <AlertDescription>
                {t(
                  'The requested rewards stay reserved until payment is recorded, or the request is rejected or cancelled.'
                )}
              </AlertDescription>
            </Alert>
          </div>
        ) : (
          <div className='space-y-5'>
            <Field>
              <FieldLabel htmlFor='withdrawal-amount'>
                {t('Withdrawal amount')} (USDT)
              </FieldLabel>
              <Input
                id='withdrawal-amount'
                value={amount}
                onChange={(e) => setAmount(e.target.value)}
                inputMode='decimal'
                disabled={busy}
                aria-invalid={submitted && !validAmount}
              />
              <p className='text-muted-foreground text-xs'>
                {t('Minimum {{minimum}} USDT · Available {{available}} USDT', {
                  minimum: quotaToUSDT(
                    summary.config.min_withdrawal_quota,
                    summary.config.quota_per_usdt
                  ),
                  available: quotaToUSDT(
                    Math.max(0, summary.available_quota),
                    summary.config.quota_per_usdt
                  ),
                })}
              </p>
              <FieldError>
                {submitted && !validAmount
                  ? t('Enter an eligible amount with up to six decimal places.')
                  : null}
              </FieldError>
            </Field>
            <Field>
              <FieldLabel htmlFor='withdrawal-network'>
                {t('Network')}
              </FieldLabel>
              <NativeSelect
                id='withdrawal-network'
                value={network}
                disabled={busy}
                onChange={(e) =>
                  setNetwork(e.target.value as WithdrawalNetwork)
                }
              >
                {(['BSC', 'POLYGON', 'TRC20'] as const).map((value) => (
                  <NativeSelectOption key={value} value={value}>
                    {value}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </Field>
            <Field>
              <FieldLabel htmlFor='withdrawal-address'>
                {t('Receiving address')}
              </FieldLabel>
              <Input
                id='withdrawal-address'
                className='font-mono text-xs'
                value={address}
                maxLength={64}
                onChange={(e) => setAddress(e.target.value)}
                disabled={busy}
                autoComplete='off'
                spellCheck={false}
                aria-invalid={submitted && !address.trim()}
              />
              <FieldError>
                {submitted && !address.trim()
                  ? t('Enter a receiving address.')
                  : null}
              </FieldError>
            </Field>
            <Alert>
              <AlertDescription>
                {t(
                  'The address must support USDT on the selected network. Review every character before confirming.'
                )}
              </AlertDescription>
            </Alert>
          </div>
        )}
      </Dialog>
      <SecureVerificationDialog {...verification.dialogProps} />
    </>
  )
}
