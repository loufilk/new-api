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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { getServerErrorMessage } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import { getInvoicePaymentOptions } from '../api'
import type { InvoiceApplication } from '../types'
import type { InvoicePaymentChoice } from './invoice-application-form'
import { InvoicePaymentMethods } from './invoice-payment-methods'

export function InvoicePaymentPanel(props: {
  invoice: InvoiceApplication
  pending: boolean
  onPay: (
    invoice: InvoiceApplication,
    choice: InvoicePaymentChoice
  ) => Promise<void>
  onRefresh: () => Promise<void>
}) {
  const { t } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const [method, setMethod] = useState('wallet')
  const [error, setError] = useState('')
  const [refreshing, setRefreshing] = useState(false)
  const options = useQuery({
    queryKey: [
      'commerce',
      'invoice-payment-options',
      userId,
      props.invoice.id,
      props.invoice.revision,
    ],
    queryFn: () =>
      getInvoicePaymentOptions({
        invoiceId: props.invoice.id,
        currency: props.invoice.currency,
        exponent: props.invoice.currency_exponent,
        feeMinor: props.invoice.fee_minor,
      }),
  })
  if (options.isPending) {
    return <LoadingState message={t('Loading payment methods...')} />
  }
  if (options.isError) {
    return <ErrorState onRetry={() => void options.refetch()} />
  }
  const methods = options.data.methods
  const chosenMethod =
    options.data.payment?.method ||
    methods.find((item) => item.type === method)?.type ||
    methods[0]?.type ||
    ''
  const insufficient =
    chosenMethod === 'wallet' &&
    BigInt(options.data.wallet_balance_quota) <
      BigInt(options.data.wallet_quota)
  const methodAvailable = methods.some((item) => item.type === chosenMethod)
  const busy = props.pending || options.isFetching || refreshing
  return (
    <section
      className='space-y-4 rounded-2xl border p-4'
      aria-label={t('Pay invoice tax fee')}
    >
      <h4 className='text-sm font-semibold'>{t('Pay invoice tax fee')}</h4>
      <InvoicePaymentMethods
        options={options.data}
        method={chosenMethod}
        disabled={busy}
        onChange={setMethod}
      />
      {error && (
        <p className='text-destructive text-sm' role='alert'>
          {error}
        </p>
      )}
      {!methods.length && (
        <p className='text-muted-foreground text-sm'>
          {t('No compatible online payment method is configured.')}
        </p>
      )}
      <div className='flex flex-wrap gap-2'>
        <Button
          disabled={busy || !methodAvailable || insufficient}
          onClick={async () => {
            setError('')
            try {
              await props.onPay(props.invoice, {
                method: chosenMethod,
                expectedWalletQuota:
                  chosenMethod === 'wallet'
                    ? options.data.wallet_quota
                    : undefined,
              })
            } catch (reason) {
              setError(getServerErrorMessage(reason))
            }
          }}
        >
          {props.pending ? t('Processing...') : t('Pay tax fee')}
        </Button>
        <Button
          variant='outline'
          disabled={busy}
          onClick={async () => {
            setRefreshing(true)
            setError('')
            try {
              await props.onRefresh()
              await options.refetch()
            } catch (reason) {
              setError(getServerErrorMessage(reason))
            } finally {
              setRefreshing(false)
            }
          }}
        >
          {refreshing ? t('Refreshing...') : t('Refresh payment status')}
        </Button>
      </div>
    </section>
  )
}
