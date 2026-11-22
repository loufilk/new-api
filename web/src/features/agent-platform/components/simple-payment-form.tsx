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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { QRCodeSVG } from 'qrcode.react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { handleServerError } from '@/lib/handle-server-error'

import { useRequestId } from '../hooks/use-request-id'
import {
  formatAgentQuota,
  parseAgentQuota,
  parseMinorMoney,
} from '../lib/money'
import {
  createSimplePayment,
  getSimplePaymentMethods,
  quoteSimplePayment,
  type SimplePaymentQuote,
  type SimplePaymentRequest,
} from '../simple-api'
import { ActionForm, type ActionField, type ActionValues } from './action-form'

type PaymentDraft = {
  payload: Omit<SimplePaymentRequest, 'request_id'>
  quote: SimplePaymentQuote
}

export function SimplePaymentForm(props: { onClose: () => void }) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const identify = useRequestId()
  const [createdURL, setCreatedURL] = useState('')
  const [initial, setInitial] = useState<ActionValues | undefined>()
  const [draft, setDraft] = useState<PaymentDraft | null>(null)
  const [creating, setCreating] = useState(false)
  const info = useQuery({
    queryKey: ['agent-platform', 'simple-payment-methods'],
    queryFn: () => getSimplePaymentMethods(),
  })
  const methods = info.data ?? []
  const yuan = (fen: number) => `¥${(fen / 100).toFixed(2)}`

  if (createdURL) {
    return (
      <Dialog
        open
        title={t('Customer payment link created')}
        description={t(
          'Send this link to the customer. The code is delivered after payment succeeds.'
        )}
        onOpenChange={(open) => {
          if (!open) props.onClose()
        }}
        footer={<Button onClick={props.onClose}>{t('Done')}</Button>}
      >
        <div className='mx-auto mb-4 w-fit rounded-lg bg-white p-3'>
          <QRCodeSVG
            value={createdURL}
            size={192}
            marginSize={4}
            role='img'
            aria-label={t('Customer payment QR code')}
          />
        </div>
        <div className='flex min-w-0 items-center gap-2 rounded-md border p-3'>
          <code className='min-w-0 flex-1 text-xs break-all'>{createdURL}</code>
          <CopyButton value={createdURL} aria-label={t('Copy payment link')} />
        </div>
        <p className='text-muted-foreground mt-3 text-xs'>
          {t('Unpaid payment links expire after seven days.')}
        </p>
      </Dialog>
    )
  }
  if (info.isPending || info.isError || !methods.length) {
    let infoContent = (
      <p className='text-muted-foreground text-sm'>
        {t('No compatible online payment method is configured.')}
      </p>
    )
    if (info.isPending) infoContent = <LoadingState />
    else if (info.isError) {
      infoContent = (
        <ErrorState
          onRetry={() => {
            void info.refetch()
          }}
        />
      )
    }
    return (
      <Dialog
        open
        title={t('Generate customer payment code')}
        onOpenChange={(open) => {
          if (!open) props.onClose()
        }}
      >
        {infoContent}
      </Dialog>
    )
  }
  if (draft) {
    const quote = draft.quote
    const confirm = async () => {
      if (creating || !quote.price_valid) return
      setCreating(true)
      try {
        const payload = { ...draft.payload, fee_version: quote.fee_version }
        const result = await createSimplePayment({
          request_id: identify(payload),
          ...payload,
        })
        if (!result.order.customer_url) {
          throw new Error(t('Payment link was not created'))
        }
        setCreatedURL(result.order.customer_url)
        await client.invalidateQueries({
          queryKey: ['agent-platform', 'simple-payment-orders'],
        })
        toast.success(t('Customer payment link created'))
      } catch (error) {
        handleServerError(error)
      } finally {
        setCreating(false)
      }
    }
    const details = [
      [t('Quota'), formatAgentQuota(quote.quota)],
      [t('Customer price (yuan)'), yuan(quote.sale_price_fen)],
      [t('Code cost'), yuan(quote.cost_price_fen)],
      [t('Payment channel fee'), yuan(quote.fee_fen)],
      [t('Minimum customer price'), yuan(quote.minimum_sale_price_fen)],
      [t('Expected payment income'), formatAgentQuota(quote.profit_quota)],
    ]
    return (
      <Dialog
        open
        title={t('Review customer payment')}
        description={t(
          'Confirm the price and expected income before sharing the payment link.'
        )}
        onOpenChange={(open) => {
          if (!open && !creating) props.onClose()
        }}
        showCloseButton={!creating}
        footer={
          <>
            <Button
              variant='outline'
              disabled={creating}
              onClick={() => setDraft(null)}
            >
              {t('Edit price')}
            </Button>
            <Button
              disabled={creating || !quote.price_valid}
              onClick={() => {
                void confirm()
              }}
            >
              {t('Create payment link')}
            </Button>
          </>
        }
      >
        <dl className='space-y-3 text-sm'>
          {details.map(([label, value]) => (
            <div key={label} className='flex justify-between gap-4'>
              <dt className='text-muted-foreground'>{label}</dt>
              <dd className='font-medium'>{value}</dd>
            </div>
          ))}
        </dl>
        {!quote.price_valid && (
          <p role='alert' className='text-destructive mt-4 text-sm'>
            {t('Minimum customer price is {{amount}}.', {
              amount: yuan(quote.minimum_sale_price_fen),
            })}
          </p>
        )}
      </Dialog>
    )
  }
  const fields: ActionField[] = [
    { name: 'name', label: 'Name', kind: 'text', max: 80 },
    {
      name: 'quota',
      label: 'Quota',
      kind: 'text',
      validate: (value) =>
        (parseAgentQuota(String(value)) ?? 0) > 0
          ? undefined
          : t('Enter a valid amount'),
    },
    {
      name: 'sale_price',
      label: 'Customer price (yuan)',
      kind: 'text',
      max: 16,
      validate: (value) =>
        parseMinorMoney(String(value), 2)
          ? undefined
          : t('Enter a valid price with up to two decimal places'),
    },
    {
      name: 'payment_method',
      label: 'Payment method',
      kind: 'select',
      options: methods.map((method) => ({
        value: method.payment_method,
        label: method.payment_method,
      })),
    },
  ]
  return (
    <ActionForm
      title={t('Generate customer payment code')}
      description={t(
        'The payment code does not consume your agent balance. Your profit is frozen for five days after payment.'
      )}
      fields={fields}
      initial={initial ?? { payment_method: methods[0].payment_method }}
      onClose={props.onClose}
      onSubmit={async (values) => {
        const quota = parseAgentQuota(String(values.quota)) ?? 0
        const salePrice = parseMinorMoney(String(values.sale_price), 2)
        if (
          !salePrice ||
          BigInt(salePrice) > BigInt(Number.MAX_SAFE_INTEGER) ||
          quota <= 0
        ) {
          throw new Error(t('Invalid payment code request'))
        }
        const payload = {
          name: String(values.name).trim(),
          quota,
          sale_price_fen: Number(salePrice),
          payment_method: String(values.payment_method),
        }
        const quote = await quoteSimplePayment(payload)
        setInitial(values)
        setDraft({ payload, quote })
      }}
      submitLabel={t('Preview price and income')}
    />
  )
}
