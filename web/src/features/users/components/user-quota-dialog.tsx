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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { getCurrencyDisplay, getCurrencyLabel } from '@/lib/currency'
import { formatQuota, parseQuotaFromDollars } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import { useQuotaAdjustment } from '../lib/use-quota-adjustment'
import type { QuotaAdjustMode } from '../types'

interface UserQuotaDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  userId: number
  currentQuota: number
  onSuccess: () => void
}

const QUOTA_MODE_LABEL_KEYS = {
  add: 'Add',
  subtract: 'Subtract',
  override: 'Override',
} as const

export function UserQuotaDialog(props: UserQuotaDialogProps) {
  const actorId = useAuthStore((s) => s.auth.user?.id ?? 0)
  if (!props.open) return null
  return (
    <QuotaForm
      key={`${actorId}:${props.userId}`}
      {...props}
      actorId={actorId}
    />
  )
}

function QuotaForm(props: UserQuotaDialogProps & { actorId: number }) {
  const { t } = useTranslation()
  const [mode, setMode] = useState<QuotaAdjustMode>('add')
  const [amount, setAmount] = useState('')
  const { blocked, pending, loading, submit } = useQuotaAdjustment(
    { actorId: props.actorId, userId: props.userId },
    () => {
      props.onOpenChange(false)
      props.onSuccess()
    }
  )

  const { meta: currencyMeta } = getCurrencyDisplay()
  const currencyLabel = getCurrencyLabel()
  const tokensOnly = currencyMeta.kind === 'tokens'

  const amountValue = Number.parseFloat(amount) || 0
  const quotaValue = parseQuotaFromDollars(Math.abs(amountValue))

  const getPreviewText = () => {
    const current = props.currentQuota
    const val = quotaValue
    switch (mode) {
      case 'add':
        return `${t('Current quota')}: ${formatQuota(current)}  +${formatQuota(val)} = ${formatQuota(current + val)}`
      case 'subtract':
        return `${t('Current quota')}: ${formatQuota(current)}  -${formatQuota(val)} = ${formatQuota(current - val)}`
      case 'override': {
        const overrideQuota = parseQuotaFromDollars(amountValue)
        return `${t('Current quota')}: ${formatQuota(current)} → ${formatQuota(overrideQuota)}`
      }
      default:
        return ''
    }
  }

  const handleConfirm = async () => {
    if (pending) return submit(pending.mode, pending.value)
    if (!amount.trim()) return
    if (quotaValue <= 0 && mode !== 'override') return
    const value =
      mode === 'override'
        ? parseQuotaFromDollars(amountValue)
        : Math.abs(quotaValue)
    // The display conversion can overflow after multiplying by quota units.
    // Never let a non-representable number become JSON null (an apparent zero).
    if (
      !Number.isFinite(amountValue) ||
      !Number.isSafeInteger(value) ||
      value < 0
    ) {
      handleServerError(undefined, t('Please enter a valid number'))
      return
    }

    await submit(mode, value)
  }

  const placeholder = tokensOnly
    ? t('Enter amount in tokens')
    : t('Enter amount in {{currency}}', { currency: currencyLabel })

  const submitLabel = pending ? t('intelligence.retrySame') : t('Confirm')
  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Adjust Quota')}
      description={
        pending || blocked
          ? undefined
          : t('Select an operation mode and enter the amount')
      }
      contentHeight='auto'
      bodyClassName='space-y-4'
      footer={
        <>
          <Button variant='outline' onClick={() => props.onOpenChange(false)}>
            {pending || blocked ? t('Close') : t('Cancel')}
          </Button>
          <Button onClick={handleConfirm} disabled={loading || blocked}>
            {loading ? t('Processing...') : submitLabel}
          </Button>
        </>
      }
    >
      {blocked && (
        <p role='alert'>
          {t(
            'Quota adjustment recovery is unavailable in this browser. Reopen it with storage enabled and check the ledger before making another adjustment.'
          )}
        </p>
      )}
      {pending && (
        <div role='status' className='space-y-2 text-sm'>
          <p>
            {t(
              'The result is unconfirmed. Retrying sends the same adjustment: it may apply if it has not already been applied. Closing does not cancel it.'
            )}
          </p>
          <p>
            {t('Mode')}: {t(QUOTA_MODE_LABEL_KEYS[pending.mode])} ·{' '}
            {t('Amount')}: {formatQuota(pending.value)} ({pending.value}{' '}
            {t('Quota units')})
          </p>
          <p className='break-all'>
            {t('Request ID')}: <code>{pending.request_id}</code>
          </p>
        </div>
      )}
      {!blocked && !pending && (
        <div className='space-y-4'>
          <div className='text-muted-foreground text-sm'>
            {getPreviewText()}
          </div>

          <div className='space-y-2'>
            <Label>{t('Mode')}</Label>
            <div className='flex gap-1'>
              {(['add', 'subtract', 'override'] as const).map((m) => (
                <Button
                  key={m}
                  type='button'
                  variant='outline'
                  size='sm'
                  className={cn(
                    mode === m &&
                      'bg-primary text-primary-foreground hover:bg-primary/90 hover:text-primary-foreground'
                  )}
                  onClick={() => {
                    setMode(m)
                    setAmount('')
                  }}
                >
                  {t(QUOTA_MODE_LABEL_KEYS[m])}
                </Button>
              ))}
            </div>
          </div>

          <div className='space-y-2'>
            <Label>
              {t('Amount')} ({currencyLabel})
            </Label>
            <Input
              type='number'
              step={tokensOnly ? 1 : 0.000001}
              min={mode === 'override' ? undefined : 0}
              placeholder={placeholder}
              value={amount}
              onChange={(e) => {
                setAmount(e.target.value)
              }}
              onKeyDown={(e) => {
                if (e.key === 'Enter') handleConfirm()
              }}
            />
          </div>
        </div>
      )}
    </Dialog>
  )
}
