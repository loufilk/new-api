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
import { useTranslation } from 'react-i18next'

import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { formatQuotaWithCurrency } from '@/lib/currency'

import type { InvoicePaymentOptions } from '../types'

export function InvoicePaymentMethods(props: {
  options: InvoicePaymentOptions
  method: string
  disabled: boolean
  onChange: (method: string) => void
}) {
  const { t } = useTranslation()
  const wallet = props.method === 'wallet'
  const insufficient =
    wallet &&
    BigInt(props.options.wallet_balance_quota) <
      BigInt(props.options.wallet_quota)
  return (
    <div className='space-y-2'>
      <p className='text-sm font-medium'>{t('Tax fee payment method')}</p>
      <ToggleGroup
        value={[props.method]}
        onValueChange={(values) => {
          if (values[0]) props.onChange(String(values[0]))
        }}
        aria-label={t('Tax fee payment method')}
        variant='outline'
        spacing={2}
        className='flex w-full flex-wrap'
        disabled={props.disabled}
      >
        {props.options.methods.map((method) => (
          <ToggleGroupItem
            key={method.type}
            value={method.type}
            className='min-w-20 flex-1 rounded-full'
            disabled={
              !!props.options.payment &&
              props.options.payment.method !== method.type
            }
          >
            {method.type === 'wallet' ? t('Balance') : method.name}
          </ToggleGroupItem>
        ))}
      </ToggleGroup>
      {wallet && (
        <p
          className={
            insufficient
              ? 'text-destructive text-xs'
              : 'text-muted-foreground text-xs'
          }
          role={insufficient ? 'alert' : undefined}
        >
          {insufficient
            ? t(
                'Paid balance is insufficient. Select another payment method or recharge your wallet.'
              )
            : t(
                '{{amount}} will be deducted from your paid balance of {{balance}}.',
                {
                  amount: formatQuotaWithCurrency(
                    Number(props.options.wallet_quota)
                  ),
                  balance: formatQuotaWithCurrency(
                    Number(props.options.wallet_balance_quota)
                  ),
                }
              )}
        </p>
      )}
      {props.options.payment?.provider === 'epay' && (
        <p className='text-muted-foreground text-xs'>
          {t('Continue the existing payment order to avoid duplicate charges.')}
        </p>
      )}
    </div>
  )
}
