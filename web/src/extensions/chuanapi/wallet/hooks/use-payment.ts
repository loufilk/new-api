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
import i18next from 'i18next'
import { useState, useCallback, useEffect, useRef } from 'react'
import { toast } from 'sonner'

import { getAuthSessionEpoch } from '@/lib/auth-session'
import { createServerError } from '@/lib/server-error-message'
import { useEpayIntent } from './use-epay-intent'
import { handleServerError } from '@/lib/handle-server-error'

import {
  calculateAmount,
  calculateStripeAmount,
  calculateWaffoAmount,
  calculateWaffoPancakeAmount,
  requestStripePayment,
  isApiSuccess,
} from '../api'
import { isStripePayment, isWaffoPayment, isWaffoPancakePayment } from '../lib'
import type { AmountRequest, AmountResponse } from '../types'

// ============================================================================
// Payment Hook
// ============================================================================

type AmountCalculator = (request: AmountRequest) => Promise<AmountResponse>

export interface PaymentAmountCalculators {
  regular: AmountCalculator
  stripe: AmountCalculator
  waffo: AmountCalculator
  waffoPancake: AmountCalculator
}

const defaultPaymentAmountCalculators: PaymentAmountCalculators = {
  regular: calculateAmount,
  stripe: calculateStripeAmount,
  waffo: calculateWaffoAmount,
  waffoPancake: calculateWaffoPancakeAmount,
}

async function requestPaymentQuote(
  topupAmount: number,
  paymentType: string,
  calculators: PaymentAmountCalculators = defaultPaymentAmountCalculators
) {
  let calculator = calculators.regular
  if (isStripePayment(paymentType)) {
    calculator = calculators.stripe
  } else if (isWaffoPayment(paymentType)) {
    calculator = calculators.waffo
  } else if (isWaffoPancakePayment(paymentType)) {
    calculator = calculators.waffoPancake
  }

  const response = await calculator({ amount: topupAmount })
  if (!isApiSuccess(response) || !response.data) {
    throw createServerError(response, i18next.t('Payment request failed'))
  }

  return {
    price: Number(response.data),
    gateway: response.epay_gateway ?? '',
    regular: calculator === calculators.regular,
  }
}
export async function requestPaymentAmount(
  topupAmount: number,
  paymentType: string,
  calculators: PaymentAmountCalculators = defaultPaymentAmountCalculators
): Promise<number> {
  return (await requestPaymentQuote(topupAmount, paymentType, calculators))
    .price
}

export function usePayment() {
  const [amount, setAmount] = useState<number>(0)
  const [calculating, setCalculating] = useState(false)
  const [processing, setProcessing] = useState(false)

  const quoteSequenceRef = useRef(0)
  const quoteRef = useRef<{
    topupAmount: number
    paymentType: string
    gateway: string
    price: number
    sessionEpoch: number
  } | null>(null)

  const invalidateQuote = useCallback(() => {
    quoteSequenceRef.current++
    quoteRef.current = null
    setAmount(0)
    setCalculating(false)
  }, [])
  const { pay: epay, ...epayRecovery } = useEpayIntent(invalidateQuote)

  useEffect(
    () => () => {
      quoteSequenceRef.current += 1
      quoteRef.current = null
    },
    []
  )

  const isQuoteCurrent = useCallback(
    (topupAmount: number, paymentType: string) => {
      const quote = quoteRef.current
      return (
        quote !== null &&
        quote.topupAmount === topupAmount &&
        quote.paymentType === paymentType &&
        quote.sessionEpoch === getAuthSessionEpoch()
      )
    },
    []
  )

  // Bind the quote to the latest amount/method selection, not response order.
  const calculatePaymentAmount = useCallback(
    async (topupAmount: number, paymentType: string) => {
      const sequence = ++quoteSequenceRef.current
      const sessionEpoch = getAuthSessionEpoch()
      const isLatest = () =>
        sequence === quoteSequenceRef.current &&
        sessionEpoch === getAuthSessionEpoch()
      quoteRef.current = null
      setAmount(0)
      setCalculating(true)
      try {
        const {
          price: calculatedAmount,
          gateway,
          regular,
        } = await requestPaymentQuote(topupAmount, paymentType)
        if (!isLatest()) return 0
        // Preserve the server's fractional price/fees; never round the quote.
        if (
          (regular && paymentType && !/^[0-9a-f]{32}$/.test(gateway)) ||
          !Number.isFinite(calculatedAmount) ||
          calculatedAmount <= 0
        ) {
          throw Error(i18next.t('Payment request failed'))
        }
        quoteRef.current = {
          topupAmount,
          paymentType,
          sessionEpoch,
          price: calculatedAmount,
          gateway,
        }
        setAmount(calculatedAmount)
        return calculatedAmount
      } catch (error) {
        if (isLatest() && topupAmount > 0)
          handleServerError(error, i18next.t('Payment request failed'))
        return 0
      } finally {
        if (isLatest()) setCalculating(false)
      }
    },
    []
  )

  // Process payment
  const processPayment = useCallback(
    async (topupAmount: number, paymentType: string) => {
      try {
        setProcessing(true)

        const isStripe = isStripePayment(paymentType)
        const amount = Math.floor(topupAmount)

        if (!isStripe) {
          if (!isQuoteCurrent(topupAmount, paymentType) || !quoteRef.current)
            return false
          return await epay(
            amount,
            paymentType,
            Math.round(quoteRef.current.price * 100),
            quoteRef.current.gateway
          )
        }
        const response = await requestStripePayment({
          amount,
          payment_method: 'stripe',
        })

        if (!isApiSuccess(response)) {
          handleServerError(response, i18next.t('Payment request failed'))
          return false
        }

        // Handle Stripe payment
        if (isStripe && response.data?.pay_link) {
          window.open(response.data.pay_link as string, '_blank')
          toast.success(i18next.t('Redirecting to payment page...'))
          return true
        }

        return false
      } catch (error) {
        handleServerError(error, i18next.t('Payment request failed'))
        return false
      } finally {
        setProcessing(false)
      }
    },
    [epay, isQuoteCurrent]
  )

  return {
    ...epayRecovery,
    amount,
    calculating,
    processing,
    calculatePaymentAmount,
    isQuoteCurrent,
    processPayment,
    setAmount,
  }
}
