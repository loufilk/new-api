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
import { useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { submitPaymentForm } from '@/lib/payment-form'
import { useAuthStore } from '@/stores/auth-store'

import { payInvoiceFee } from '../api'
import type { InvoicePaymentChoice } from '../components/invoice-application-form'
import type { InvoiceApplication } from '../types'

export function useInvoicePayment() {
  const { t } = useTranslation()
  const client = useQueryClient()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const attempts = useRef(new Map<string, string>())
  return useMutation({
    mutationFn: async (input: {
      invoice: InvoiceApplication
      choice: InvoicePaymentChoice
    }) => {
      const signature = JSON.stringify([
        userId,
        input.invoice.id,
        input.invoice.revision,
        input.choice,
      ])
      let key = attempts.current.get(signature)
      if (!key) {
        key = crypto.randomUUID()
        attempts.current.set(signature, key)
      }
      return payInvoiceFee(
        input.invoice.id,
        input.choice.method,
        input.invoice.revision,
        key,
        input.choice.expectedWalletQuota
      )
    },
    onSuccess: async (result) => {
      client.setQueryData(
        ['commerce', 'invoice', userId, false, result.invoice.id],
        result.invoice
      )
      if (result.url && result.params) {
        const url = new URL(result.url)
        if (
          !['https:', 'http:'].includes(url.protocol) ||
          url.username ||
          url.password
        ) {
          throw new Error(
            t(
              'The payment link is invalid. Refresh the application and try again.'
            )
          )
        }
        submitPaymentForm(url.href, result.params)
        toast.success(
          t(
            'The payment page has opened. Refresh this application after paying.'
          )
        )
      } else if (result.invoice.status !== 'awaiting_fee') {
        toast.success(t('Tax fee paid. Your invoice is under review.'))
      }
      await Promise.all([
        client.invalidateQueries({ queryKey: ['commerce'] }),
        client.invalidateQueries({ queryKey: ['wallet'] }),
      ])
    },
  })
}
