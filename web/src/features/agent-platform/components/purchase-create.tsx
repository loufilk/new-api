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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { handleServerError } from '@/lib/handle-server-error'

import { postPlatform } from '../api'
import { useRequestId } from '../hooks/use-request-id'
import type { PurchaseQuote } from '../types'
import { ActionForm } from './action-form'
import { PriceSnapshot } from './price-snapshot'
import { BusinessDate, Money, Quota } from './values'

export function PurchaseCreate(props: { onClose: () => void }) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const quoteRequest = useRequestId()
  const orderRequest = useRequestId()
  const locked = useRef(false)
  const [quote, setQuote] = useState<PurchaseQuote | null>(null)
  const create = useMutation({
    mutationFn: async (current: PurchaseQuote) =>
      postPlatform('/purchases', {
        request_id: orderRequest({ quote_id: current.id }),
        quote_id: current.id,
      }),
    retry: false,
    onSuccess: () => {
      toast.success(
        t('Procurement order created. Submit payment evidence after paying.')
      )
      void client.invalidateQueries({ queryKey: ['agent-platform'] })
      props.onClose()
    },
    onError: (error) => handleServerError(error),
    onSettled: () => {
      locked.current = false
    },
  })
  return (
    <>
      {!quote && (
        <ActionForm
          title={t('Request procurement quote')}
          description={t(
            'A quote or payment declaration never adds procurement quota. Two independent finance operators must verify the actual receipt.'
          )}
          fields={[
            {
              name: 'quota',
              label: 'Procurement quota units',
              kind: 'integer',
              min: 1,
            },
          ]}
          onClose={props.onClose}
          onSubmit={async (values) => {
            const quota = Number(values.quota)
            setQuote(
              await postPlatform<PurchaseQuote>('/purchases/quotes', {
                request_id: quoteRequest({ quota }),
                quota,
              })
            )
          }}
        />
      )}
      {quote && (
        <ConfirmDialog
          open
          title={t('Confirm procurement quote')}
          desc={t(
            'This creates an order, not a payment. The quoted currency, cost and policy are fixed for this order.'
          )}
          onOpenChange={(open) => {
            if (!open && !create.isPending) setQuote(null)
          }}
          confirmText={t('Create procurement order')}
          isLoading={create.isPending}
          handleConfirm={() => {
            if (locked.current) return
            if (quote.expires_at * 1000 <= Date.now()) {
              handleServerError(
                new Error(t('The quote expired. Request a new quote.'))
              )
              quoteRequest.reset()
              setQuote(null)
              return
            }
            locked.current = true
            create.mutate(quote)
          }}
        >
          <dl className='grid grid-cols-2 gap-3 text-sm'>
            <div>
              <dt>{t('You pay')}</dt>
              <dd>
                <Money
                  minor={quote.paid_minor}
                  currency={quote.currency}
                  exponent={quote.currency_exponent}
                />
              </dd>
            </div>
            <div>
              <dt>{t('Procurement quota')}</dt>
              <dd>
                <Quota value={quote.quota} />
              </dd>
            </div>
            <div>
              <dt>{t('Policy version')}</dt>
              <dd>{quote.policy_version}</dd>
            </div>
            <div>
              <dt>{t('Quote expires')}</dt>
              <dd>
                <BusinessDate value={quote.expires_at} />
              </dd>
            </div>
          </dl>
          <PriceSnapshot value={quote.price_snapshot} />
        </ConfirmDialog>
      )}
    </>
  )
}
