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
import { Gift, Loader2 } from 'lucide-react'
import { useId } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'

import { getPaymentIcon } from '../wallet/lib'
import { formatBenefitCny } from './lib'
import type { BenefitOffer } from './types'

interface BenefitDialogProps {
  offer: BenefitOffer
  selectedMethod: string
  processing: boolean
  notice?: string
  onMethodChange: (method: string) => void
  onOpenChange: (open: boolean) => void
  onConfirm: () => void
}

export function BenefitDialog(props: BenefitDialogProps) {
  const { t, i18n } = useTranslation()
  const methodLabelId = useId()
  const locale = i18n.resolvedLanguage || i18n.language
  const pay = formatBenefitCny(props.offer.payAmount, locale)
  const credit = formatBenefitCny(props.offer.creditAmount, locale)
  const methodAvailable = props.offer.methods.some(
    (method) => method.type === props.selectedMethod
  )

  return (
    <ConfirmDialog
      open
      onOpenChange={(open) => {
        if (!props.processing) props.onOpenChange(open)
      }}
      title={
        <span className='flex items-center gap-2'>
          <Gift className='text-primary size-5' aria-hidden='true' />
          {t('New user recharge benefit')}
        </span>
      }
      desc={t(
        'Pay {{pay}} to receive {{credit}} in total credit after payment.',
        {
          pay,
          credit,
        }
      )}
      confirmText={
        <span className='flex items-center justify-center gap-2'>
          {props.processing && (
            <Loader2 className='size-4 animate-spin' aria-hidden='true' />
          )}
          {t('Pay {{amount}} and continue', { amount: pay })}
        </span>
      }
      isLoading={props.processing}
      disabled={!methodAvailable}
      handleConfirm={props.onConfirm}
      className='max-h-[calc(100svh-2rem)] overflow-y-auto sm:max-w-md'
    >
      <div className='space-y-4'>
        {props.notice && (
          <Alert>
            <AlertDescription>{t(props.notice)}</AlertDescription>
          </Alert>
        )}
        {props.offer.orderId && (
          <p className='text-muted-foreground text-sm'>
            {t('Continue your existing benefit order at its original price.')}
          </p>
        )}
        <p className='text-muted-foreground text-sm'>
          {t(
            'The payment amount goes to your paid balance. The remaining credit goes to your bonus balance and can only be used with models that allow bonus credit.'
          )}
        </p>
        {props.offer.methods.length > 0 ? (
          <div className='space-y-2'>
            <p id={methodLabelId} className='text-sm font-medium'>
              {t('Payment Method')}
            </p>
            <div
              className='grid grid-cols-1 gap-2 sm:grid-cols-2'
              role='group'
              aria-labelledby={methodLabelId}
            >
              {props.offer.methods.map((method) => (
                <Button
                  key={method.type}
                  type='button'
                  variant={
                    props.selectedMethod === method.type
                      ? 'secondary'
                      : 'outline'
                  }
                  aria-pressed={props.selectedMethod === method.type}
                  disabled={props.processing}
                  className='h-auto min-h-9 justify-start py-2 text-start whitespace-normal'
                  onClick={() => props.onMethodChange(method.type)}
                >
                  <span aria-hidden='true' className='shrink-0'>
                    {getPaymentIcon(
                      method.type,
                      'size-4',
                      method.icon,
                      method.name
                    )}
                  </span>
                  {method.name}
                </Button>
              ))}
            </div>
          </div>
        ) : (
          <Alert>
            <AlertDescription>
              {t(
                'This benefit payment method is currently unavailable. Please try again later.'
              )}
            </AlertDescription>
          </Alert>
        )}
        <p className='text-muted-foreground text-xs'>
          {t(
            'One benefit per account before its first successful recharge. Amounts are in CNY.'
          )}
        </p>
      </div>
    </ConfirmDialog>
  )
}
