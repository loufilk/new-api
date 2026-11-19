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
import { useEffect, useRef, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { getAuthSessionEpoch } from '@/lib/auth-session'
import { submitPaymentForm } from '@/lib/payment-form'
import { getServerErrorMessage } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import type { InvoiceDetail } from '../contracts'
import { invoiceAPI, invoiceKey } from './api'
import { useInvoiceText } from './copy'
import type { FeePaymentMethod } from './policy'

export function FeePayment(props: {
  owner: number
  detail: InvoiceDetail
  methods: FeePaymentMethod[]
  disabled: boolean
  onBusy: (busy: boolean) => void
}) {
  const { text, money } = useInvoiceText()
  const client = useQueryClient()
  const invoice = props.detail
  const fee = invoice.fee_minor ?? ''
  const quota = invoice.wallet_fee_quota ?? ''
  const rate = invoice.wallet_quota_per_cent ?? 0
  const exactFee = /^[1-9]\d{0,18}$/.test(fee)
  const validWallet =
    exactFee &&
    Number.isSafeInteger(rate) &&
    rate > 0 &&
    /^[1-9]\d{0,18}$/.test(quota) &&
    BigInt(quota) <= BigInt(Number.MAX_SAFE_INTEGER) &&
    BigInt(fee) * BigInt(rate) === BigInt(quota)
  const checkout = invoice.fee_state === 'checkout'
  const choices = props.methods.filter((method) => {
    if (!exactFee || invoice.status !== 'awaiting_payment') return false
    if (checkout) {
      return (
        method.rail === 'epay' &&
        invoice.fee_rail === 'epay' &&
        method.id === invoice.fee_payment_method
      )
    }
    return (
      invoice.fee_state === 'unpaid' &&
      !invoice.fee_rail &&
      (method.rail === 'epay' || validWallet)
    )
  })
  const [choice, setChoice] = useState('')
  const selected = choices.find(
    (method) => checkout || choice === `${method.rail}:${method.id}`
  )
  const mounted = useRef(false)
  const working = useRef(false)
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])
  const refresh = () =>
    client.invalidateQueries({ queryKey: invoiceKey(props.owner) })
  const mutation = useMutation({
    mutationFn: async (method: FeePaymentMethod) => {
      const epoch = getAuthSessionEpoch()
      if (method.rail === 'wallet') {
        await invoiceAPI.walletFee(invoice.id, fee, quota, rate)
      } else {
        const form = await invoiceAPI.epayFee(invoice.id, fee, method.id)
        if (
          mounted.current &&
          epoch === getAuthSessionEpoch() &&
          props.owner === useAuthStore.getState().auth.user?.id
        ) {
          submitPaymentForm(form.url, form.params)
        }
      }
    },
    gcTime: 0,
    retry: false,
    meta: { errorToast: false },
    onSettled: async () => {
      try {
        await refresh()
      } finally {
        working.current = false
        props.onBusy(false)
      }
    },
  })
  const locked = props.disabled || mutation.isPending
  let payLabel = text('Choose a payment method')
  if (selected?.rail === 'wallet') {
    payLabel = text('Use {{quota}} paid quota units to pay {{fee}}', {
      quota,
      fee: money(fee),
    })
  } else if (selected) {
    payLabel = text('Pay {{fee}} with {{method}}', {
      fee: money(fee),
      method: selected.name || selected.id,
    })
  }
  return (
    <div className='my-4 space-y-3 rounded-lg border p-3'>
      {choices.length ? (
        <>
          <Label htmlFor='invoice-fee-method'>
            {text('Fee payment method')}
          </Label>
          <NativeSelect
            className='w-full'
            id='invoice-fee-method'
            disabled={locked || checkout}
            value={selected ? `${selected.rail}:${selected.id}` : ''}
            onChange={(e) => setChoice(e.target.value)}
          >
            <NativeSelectOption value='' disabled>
              {text('Choose a payment method')}
            </NativeSelectOption>
            {choices.map((method) => (
              <NativeSelectOption
                key={`${method.rail}:${method.id}`}
                value={`${method.rail}:${method.id}`}
              >
                {method.rail === 'wallet'
                  ? text('Paid wallet balance')
                  : method.name || method.id}
              </NativeSelectOption>
            ))}
          </NativeSelect>
          {selected?.rail === 'wallet' && (
            <p className='text-muted-foreground text-xs'>
              {text('Only paid quota is used, not bonus or reward quota.')}
            </p>
          )}
          <Button
            disabled={!selected || locked}
            onClick={() => {
              if (!selected || locked || working.current) return
              working.current = true
              props.onBusy(true)
              mutation.mutate(selected)
            }}
          >
            {payLabel}
          </Button>
        </>
      ) : (
        <p className='text-muted-foreground text-sm'>
          {text('No payment method is currently available.')}
        </p>
      )}
      {(checkout || (mutation.isSuccess && selected?.rail === 'epay')) && (
        <p role='status' className='text-muted-foreground text-sm'>
          {text(
            'Complete payment with the provider, then refresh the status here.'
          )}
        </p>
      )}
      {mutation.isError && (
        <p role='alert' className='text-destructive text-sm'>
          {getServerErrorMessage(mutation.error)}
        </p>
      )}
      <Button
        size='sm'
        variant='outline'
        className='block'
        disabled={locked}
        onClick={() => void refresh()}
      >
        {text('Refresh payment status')}
      </Button>
    </div>
  )
}
