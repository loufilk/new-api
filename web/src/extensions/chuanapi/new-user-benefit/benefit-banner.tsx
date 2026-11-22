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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Gift, X } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import type { WalletBenefitBannerProps } from '@/extensions/wallet'
import { getAuthSessionEpoch } from '@/lib/auth-session'
import { handleServerError } from '@/lib/handle-server-error'
import { submitPaymentForm } from '@/lib/payment-form'
import { useAuthStore } from '@/stores/auth-store'

import {
  getNewUserBenefit,
  requestBenefitPayment,
  recheckBenefitPaymentIdentity,
} from './api'
import { BenefitDialog } from './benefit-dialog'
import {
  BENEFIT_QUERY_KEY,
  benefitBlockedMessage,
  benefitErrorMessage,
  createBenefitIdempotencyKey,
  formatBenefitCny,
  getBenefitOffer,
  isBenefitConfigConflict,
} from './lib'
import type {
  BenefitOffer,
  BenefitPaymentRequest,
  NewUserBenefitInfo,
} from './types'

interface BenefitBannerForUserProps extends WalletBenefitBannerProps {
  userId: number
}

function BenefitBannerForUser(props: BenefitBannerForUserProps) {
  const { t, i18n } = useTranslation()
  const client = useQueryClient()
  const epoch = getAuthSessionEpoch()
  const [dismissed, setDismissed] = useState(false)
  const [dialogOffer, setDialogOffer] = useState<BenefitOffer | null>(null)
  const [selectedMethod, setSelectedMethod] = useState('')
  const [notice, setNotice] = useState<string>()
  const inFlight = useRef(false)
  const mounted = useRef(true)
  const attempt = useRef<{ identity: string; key: string } | null>(null)
  const query = useQuery({
    queryKey: [...BENEFIT_QUERY_KEY, props.userId, epoch],
    queryFn: ({ signal }) => getNewUserBenefit(signal),
    staleTime: 30_000,
    retry: 1,
    refetchOnWindowFocus: 'always',
    meta: { errorToast: false, redirectOnInternalServerError: false },
  })
  const payment = useMutation({
    mutationFn: (variables: {
      request: BenefitPaymentRequest
      key: string
      turnstile: string
    }) =>
      requestBenefitPayment(
        variables.request,
        variables.key,
        variables.turnstile
      ),
    retry: false,
    meta: { errorToast: false },
  })
  const recheck = useMutation({
    mutationFn: recheckBenefitPaymentIdentity,
    retry: false,
    meta: { errorToast: false },
  })
  const rechecking = useRef(false)
  const previousStatus = useRef<NewUserBenefitInfo['status'] | undefined>(
    undefined
  )

  useEffect(() => {
    const status = query.data?.status
    const previous = previousStatus.current
    if (status !== undefined) previousStatus.current = status
    if (
      status !== previous &&
      (status === 'claimed' ||
        status === 'verification_pending' ||
        status === 'reward_denied') &&
      previous !== undefined
    ) {
      void client.invalidateQueries({ queryKey: ['wallet'] })
    }
  }, [query.data?.status, client])

  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])

  const offer = getBenefitOffer(query.data)
  const locale = i18n.resolvedLanguage || i18n.language

  const openConfirmation = (nextOffer: BenefitOffer) => {
    setDialogOffer(nextOffer)
    setSelectedMethod(nextOffer.methods[0]?.type ?? '')
    setNotice(undefined)
  }

  const confirmPayment = async (turnstile: string) => {
    if (
      inFlight.current ||
      !dialogOffer ||
      (query.data?.turnstile_required && !turnstile) ||
      !dialogOffer.methods.some((method) => method.type === selectedMethod)
    ) {
      return
    }
    const requestEpoch = getAuthSessionEpoch()
    const isCurrent = () =>
      mounted.current &&
      requestEpoch === getAuthSessionEpoch() &&
      useAuthStore.getState().auth.user?.id === props.userId
    const identity = `${requestEpoch}:${dialogOffer.version}:${selectedMethod}`
    if (attempt.current?.identity !== identity) {
      attempt.current = { identity, key: createBenefitIdempotencyKey() }
    }
    inFlight.current = true
    try {
      const checkout = await payment.mutateAsync({
        request: {
          payment_method: selectedMethod,
          config_version: dialogOffer.version,
        },
        key: attempt.current.key,
        turnstile,
      })
      if (!isCurrent()) return
      // Never forward the user to a payment amount they did not confirm.
      if (
        checkout.pay_amount_cny !== dialogOffer.payAmount ||
        checkout.credit_amount_cny !== dialogOffer.creditAmount ||
        (dialogOffer.orderId && checkout.order_id !== dialogOffer.orderId)
      ) {
        setNotice(
          'The offer changed. Review the updated amounts and confirm again.'
        )
        const updated = await query.refetch()
        if (!isCurrent()) return
        const updatedOffer = getBenefitOffer(updated.data)
        setDialogOffer(updatedOffer)
        setSelectedMethod(updatedOffer?.methods[0]?.type ?? '')
        if (!updatedOffer) {
          toast.error(t('New user benefit is temporarily unavailable.'))
        }
        return
      }
      const url = new URL(checkout.checkout.url)
      if (
        checkout.checkout.kind !== 'form' ||
        !['https:', 'http:'].includes(url.protocol)
      ) {
        throw new Error('Payment request failed')
      }
      submitPaymentForm(url.href, checkout.checkout.params)
      setDialogOffer(null)
      toast.success(t('Redirecting to payment page...'))
      await client.invalidateQueries({ queryKey: BENEFIT_QUERY_KEY })
    } catch (error) {
      if (!isCurrent()) return
      if (isBenefitConfigConflict(error)) {
        attempt.current = null
        const updated = await query.refetch()
        if (!isCurrent()) return
        const updatedOffer = getBenefitOffer(updated.data)
        setDialogOffer(updatedOffer)
        setSelectedMethod(updatedOffer?.methods[0]?.type ?? '')
        if (!updatedOffer) {
          toast.error(t('New user benefit is temporarily unavailable.'))
          return
        }
        setNotice(
          'The offer changed. Review the updated amounts and confirm again.'
        )
      } else {
        handleServerError(error, t('Payment request failed'), {
          title: t(benefitErrorMessage(error, 'Payment request failed')),
        })
        // A lost response may still have reserved an order. Recover its fixed
        // payment method and amounts before another user-initiated attempt.
        const updated = await query.refetch()
        if (!isCurrent() || updated.isError) return
        const recoveredOffer = getBenefitOffer(updated.data)
        setDialogOffer(recoveredOffer)
        setSelectedMethod(recoveredOffer?.methods[0]?.type ?? '')
      }
    } finally {
      inFlight.current = false
    }
  }

  const recheckIdentity = async () => {
    if (rechecking.current) return
    rechecking.current = true
    const requestEpoch = getAuthSessionEpoch()
    try {
      const updated = await recheck.mutateAsync()
      if (
        !mounted.current ||
        requestEpoch !== getAuthSessionEpoch() ||
        useAuthStore.getState().auth.user?.id !== props.userId
      ) {
        return
      }
      client.setQueryData(
        [...BENEFIT_QUERY_KEY, props.userId, requestEpoch],
        updated
      )
      void client.invalidateQueries({ queryKey: ['wallet'] })
      if (updated.status === 'claimed') {
        toast.success(t('Benefit bonus credited.'))
      }
    } catch (error) {
      if (!mounted.current || requestEpoch !== getAuthSessionEpoch()) return
      handleServerError(error, t('Failed to verify payment identity'))
    } finally {
      rechecking.current = false
    }
  }

  if (
    props.persistent &&
    (query.data?.status === 'verification_pending' ||
      query.data?.status === 'reward_denied')
  ) {
    const verificationPending = query.data.status === 'verification_pending'
    return (
      <div className='shrink-0 px-3 pt-3 sm:px-4'>
        <Alert role='region' aria-label={t('New user recharge benefit')}>
          <AlertTitle>
            {verificationPending
              ? t('Benefit bonus pending verification')
              : t('Benefit bonus not issued')}
          </AlertTitle>
          <AlertDescription>
            <p>
              {verificationPending
                ? t(
                    'Your payment is credited. The bonus is waiting for payment identity verification.'
                  )
                : t(
                    'This payment identity has already received the benefit. Your payment is credited; no bonus was issued.'
                  )}
            </p>
            {verificationPending && (
              <Button
                type='button'
                size='sm'
                variant='outline'
                disabled={recheck.isPending}
                onClick={() => void recheckIdentity()}
              >
                {recheck.isPending
                  ? t('Verifying payment identity...')
                  : t('Recheck payment identity')}
              </Button>
            )}
          </AlertDescription>
        </Alert>
      </div>
    )
  }

  if (!offer) {
    if (
      !props.persistent ||
      !query.data?.enabled ||
      !query.data.blocked_reason
    ) {
      return null
    }
    return (
      <div className='shrink-0 px-3 pt-3 sm:px-4'>
        <Alert role='region' aria-label={t('New user recharge benefit')}>
          <AlertTitle>{t('New user recharge benefit')}</AlertTitle>
          <AlertDescription>
            <p>{t(benefitBlockedMessage(query.data.blocked_reason))}</p>
            {query.data.blocked_reason === 'email_verification_required' && (
              <Button
                size='sm'
                variant='outline'
                role='link'
                render={<Link to='/security' />}
              >
                {t('Verify email in account security')}
              </Button>
            )}
          </AlertDescription>
        </Alert>
      </div>
    )
  }
  if (dismissed && !props.persistent && !dialogOffer) return null

  const pay = formatBenefitCny(offer.payAmount, locale)
  const credit = formatBenefitCny(offer.creditAmount, locale)

  return (
    <>
      <div className='shrink-0 px-3 pt-3 sm:px-4'>
        <Alert
          role='region'
          aria-label={t('New user recharge benefit')}
          className='border-primary/20 bg-primary/5 grid-cols-1 gap-3 rounded-xl p-3 sm:flex sm:items-center sm:gap-4'
        >
          <div className='flex min-w-0 flex-1 items-start gap-3 pr-8 sm:items-center sm:pr-0'>
            <div className='bg-primary/10 text-primary flex size-10 shrink-0 items-center justify-center rounded-xl'>
              <Gift className='size-5' aria-hidden='true' />
            </div>
            <div className='min-w-0 space-y-1'>
              <AlertTitle>
                <Badge variant='secondary'>{t('New user exclusive')}</Badge>
              </AlertTitle>
              <AlertDescription className='text-foreground text-sm'>
                {t(
                  offer.paymentIdentityCheckEnabled
                    ? 'Pay {{pay}} for {{credit}} in total credit, including a bonus subject to payment identity verification.'
                    : 'Pay {{pay}} and get {{credit}} in total credit',
                  {
                    pay,
                    credit,
                  }
                )}
              </AlertDescription>
            </div>
          </div>
          <Button
            type='button'
            size='sm'
            className='h-auto min-h-9 w-full shrink-0 py-2 whitespace-normal sm:w-auto'
            onClick={() => openConfirmation(offer)}
          >
            <Gift className='size-4 shrink-0' aria-hidden='true' />
            {offer.orderId
              ? t('Continue benefit payment')
              : t('View recharge benefit')}
          </Button>
          {!props.persistent && (
            <Button
              type='button'
              variant='ghost'
              size='icon-sm'
              className='absolute top-2 right-2 sm:static sm:shrink-0'
              aria-label={t('Dismiss recharge benefit banner')}
              onClick={() => setDismissed(true)}
            >
              <X className='size-4' aria-hidden='true' />
            </Button>
          )}
        </Alert>
      </div>
      {dialogOffer && (
        <BenefitDialog
          key={`${dialogOffer.version}:${dialogOffer.orderId ?? ''}:${Boolean(query.data?.turnstile_required)}:${query.data?.turnstile_site_key ?? ''}`}
          offer={dialogOffer}
          selectedMethod={selectedMethod}
          onMethodChange={setSelectedMethod}
          processing={payment.isPending}
          notice={
            notice ??
            (query.data?.blocked_reason
              ? benefitBlockedMessage(query.data.blocked_reason)
              : undefined)
          }
          turnstileRequired={query.data?.turnstile_required === true}
          turnstileSiteKey={query.data?.turnstile_site_key ?? ''}
          onOpenChange={(open) => {
            if (!open) setDialogOffer(null)
          }}
          onConfirm={(turnstile) => void confirmPayment(turnstile)}
        />
      )}
    </>
  )
}

export function NewUserBenefitBanner(props: WalletBenefitBannerProps) {
  const userId = useAuthStore((state) => state.auth.user?.id)
  const sessionId = useAuthStore((state) => state.auth.session?.sid)
  if (!userId) return null
  return (
    <BenefitBannerForUser
      key={`${userId}:${sessionId ?? ''}`}
      userId={userId}
      persistent={props.persistent}
    />
  )
}
