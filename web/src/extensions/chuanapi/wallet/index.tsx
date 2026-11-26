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
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import type { WalletPageProps } from '@/extensions/wallet'
import { useStatus } from '@/hooks/use-status'
import { useSystemConfig } from '@/hooks/use-system-config'
import { getSelf } from '@/lib/api'
import { handleServerError } from '@/lib/handle-server-error'

import { CreemConfirmDialog } from './components/dialogs/creem-confirm-dialog'
import { PaymentConfirmDialog } from './components/dialogs/payment-confirm-dialog'
import { LedgerTable } from './components/ledger-table'
import { RechargeFormCard } from './components/recharge-form-card'
import { RechargeHistoryButton } from './components/recharge-history-dialog'
import { ReferralPanel } from './components/referral-panel'
import { UsdtRecharge } from './components/usdt-recharge'
import { WalletStatsCard } from './components/wallet-stats-card'
import { DEFAULT_DISCOUNT_RATE, PAYMENT_TYPES } from './constants'
import {
  useCreemPayment,
  usePayment,
  useRedemption,
  useTopupInfo,
  useWaffoPancakePayment,
  useWaffoPayment,
  useWalletSummary,
} from './hooks'
import {
  dispatchSelectedPayment,
  getDefaultPaymentType,
  getMinTopupAmount,
} from './lib'
import type {
  CreemProduct,
  PaymentMethod,
  PresetAmount,
  UserWalletData,
  WaffoPayMethod,
} from './types'

export function Wallet(props: WalletPageProps) {
  const { t } = useTranslation()
  const [user, setUser] = useState<UserWalletData | null>(null)
  const [userLoading, setUserLoading] = useState(true)
  const [topupAmount, setTopupAmount] = useState(0)
  const [selectedPreset, setSelectedPreset] = useState<number | null>(null)
  const [selectedPaymentMethod, setSelectedPaymentMethod] =
    useState<PaymentMethod>()
  const [selectedWaffoMethodIndex, setSelectedWaffoMethodIndex] = useState<
    number | null
  >(null)
  const [paymentLoading, setPaymentLoading] = useState<string | null>(null)
  const [confirmDialogOpen, setConfirmDialogOpen] = useState(false)
  const [redemptionCode, setRedemptionCode] = useState('')
  const [creemDialogOpen, setCreemDialogOpen] = useState(false)
  const [selectedCreemProduct, setSelectedCreemProduct] =
    useState<CreemProduct | null>(null)

  const { status } = useStatus()
  const { currency } = useSystemConfig()
  const { topupInfo, presetAmounts, loading: topupLoading } = useTopupInfo()
  const {
    summary,
    loading: walletLoading,
    error: walletError,
    refetch: refetchWallet,
  } = useWalletSummary()
  const {
    recovery,
    checkEpay,
    forgetEpay,
    invalidateEpayHandoff,
    amount: paymentAmount,
    calculating,
    processing,
    calculatePaymentAmount,
    isQuoteCurrent,
    processPayment,
  } = usePayment()
  const recoveryStatus =
    recovery &&
    (recovery.tradeNo ? (
      <p>
        {recovery.tradeNo} ·{' '}
        {t(
          (
            {
              pending: 'Pending',
              success: 'Success',
              failed: 'Failed',
              expired: 'Expired',
            } as Record<string, string>
          )[recovery.status]
        )}
      </p>
    ) : (
      <div>
        <p>
          {t(
            'Order not confirmed. Retry the same checkout to avoid duplicate orders.'
          )}
        </p>
        <p>
          {t(
            'If it stays unconfirmed, check recharge history or give support this reference:'
          )}{' '}
          <code className='break-all'>
            {recovery.reference ?? recovery.draft.id}
          </code>
        </p>
      </div>
    ))
  const { redeeming, redeemCode } = useRedemption()
  const { processing: creemProcessing, processCreemPayment } = useCreemPayment()
  const { processing: waffoProcessing, processWaffoPayment } = useWaffoPayment()
  const { processing: pancakeProcessing, processWaffoPancakePayment } =
    useWaffoPancakePayment()

  const effectiveUsdExchangeRate = useMemo(
    () =>
      currency?.quotaDisplayType === 'USD' ? 1 : currency?.usdExchangeRate || 1,
    [currency?.quotaDisplayType, currency?.usdExchangeRate]
  )

  const fetchUser = useCallback(async () => {
    try {
      setUserLoading(true)
      const response = await getSelf()
      if (response.success && response.data) {
        setUser(response.data as UserWalletData)
      }
    } catch (error) {
      // A failed focus refresh does not invalidate the previous user snapshot.
      handleServerError(error)
    } finally {
      setUserLoading(false)
    }
  }, [])

  useEffect(() => {
    let mounted = true
    queueMicrotask(() => {
      if (mounted) void fetchUser()
    })
    return () => {
      mounted = false
    }
  }, [fetchUser])

  const paymentSelectionRef = useRef(0)
  const topupAmountInitializedRef = useRef(false)
  const refreshBalancesPromiseRef = useRef<Promise<void> | null>(null)
  useEffect(() => {
    if (!topupInfo || topupAmountInitializedRef.current) return

    topupAmountInitializedRef.current = true
    const minTopup = getMinTopupAmount(topupInfo)
    setTopupAmount(minTopup)
    void calculatePaymentAmount(minTopup, getDefaultPaymentType(topupInfo))
  }, [topupInfo, calculatePaymentAmount])

  const refreshBalances = useCallback(async () => {
    if (refreshBalancesPromiseRef.current) {
      return refreshBalancesPromiseRef.current
    }

    const refreshPromise = Promise.all([fetchUser(), refetchWallet()]).then(
      () => undefined
    )
    refreshBalancesPromiseRef.current = refreshPromise
    try {
      await refreshPromise
    } finally {
      if (refreshBalancesPromiseRef.current === refreshPromise) {
        refreshBalancesPromiseRef.current = null
      }
    }
  }, [fetchUser, refetchWallet])

  useEffect(() => {
    const refreshWhenVisible = () => {
      if (document.visibilityState === 'visible') {
        void refreshBalances()
      }
    }

    window.addEventListener('focus', refreshWhenVisible)
    document.addEventListener('visibilitychange', refreshWhenVisible)
    return () => {
      window.removeEventListener('focus', refreshWhenVisible)
      document.removeEventListener('visibilitychange', refreshWhenVisible)
    }
  }, [refreshBalances])

  const getCurrentPaymentType = useCallback(
    () => selectedPaymentMethod?.type || getDefaultPaymentType(topupInfo),
    [selectedPaymentMethod, topupInfo]
  )

  const handleSelectPreset = (preset: PresetAmount) => {
    paymentSelectionRef.current += 1
    setPaymentLoading(null)
    setTopupAmount(preset.value)
    setSelectedPreset(preset.value)
    void calculatePaymentAmount(preset.value, getCurrentPaymentType())
  }

  const handleTopupAmountChange = (amount: number) => {
    paymentSelectionRef.current += 1
    setPaymentLoading(null)
    setTopupAmount(amount)
    setSelectedPreset(null)
    void calculatePaymentAmount(amount, getCurrentPaymentType())
  }

  const handlePaymentMethodSelect = async (method: PaymentMethod) => {
    const selection = ++paymentSelectionRef.current
    setSelectedPaymentMethod(method)
    setSelectedWaffoMethodIndex(null)
    setPaymentLoading(method.type)

    try {
      if (topupAmount < getMinTopupAmount(topupInfo)) return

      await calculatePaymentAmount(topupAmount, method.type)
      if (
        selection === paymentSelectionRef.current &&
        isQuoteCurrent(topupAmount, method.type)
      ) {
        setConfirmDialogOpen(true)
      }
    } finally {
      if (selection === paymentSelectionRef.current) setPaymentLoading(null)
    }
  }

  const handlePaymentConfirm = async () => {
    if (
      !selectedPaymentMethod ||
      !isQuoteCurrent(topupAmount, selectedPaymentMethod.type)
    ) {
      return
    }

    const success = await dispatchSelectedPayment(
      selectedPaymentMethod,
      topupAmount,
      selectedWaffoMethodIndex,
      {
        regular: processPayment,
        waffo: processWaffoPayment,
        waffoPancake: processWaffoPancakePayment,
      }
    )
    if (!success) return

    setConfirmDialogOpen(false)
    await refreshBalances()
  }

  const handleRedeem = async () => {
    if (!redemptionCode.trim()) return

    const success = await redeemCode(redemptionCode)
    if (!success) return

    setRedemptionCode('')
    await refreshBalances()
  }

  const handleCreemProductSelect = (product: CreemProduct) => {
    setSelectedCreemProduct(product)
    setCreemDialogOpen(true)
  }

  const handleCreemConfirm = async () => {
    if (!selectedCreemProduct) return

    const success = await processCreemPayment(selectedCreemProduct.productId)
    if (!success) return

    setCreemDialogOpen(false)
    setSelectedCreemProduct(null)
    await refreshBalances()
  }

  const handleWaffoMethodSelect = async (
    method: WaffoPayMethod,
    index: number
  ) => {
    const selection = ++paymentSelectionRef.current
    const loadingKey = `waffo-${index}`
    setSelectedPaymentMethod({
      name: method.name,
      type: PAYMENT_TYPES.WAFFO,
      icon: method.icon,
    })
    setSelectedWaffoMethodIndex(index)
    setPaymentLoading(loadingKey)

    try {
      await calculatePaymentAmount(topupAmount, PAYMENT_TYPES.WAFFO)
      if (
        selection === paymentSelectionRef.current &&
        isQuoteCurrent(topupAmount, PAYMENT_TYPES.WAFFO)
      ) {
        setConfirmDialogOpen(true)
      }
    } finally {
      if (selection === paymentSelectionRef.current) setPaymentLoading(null)
    }
  }

  const discountRate =
    topupInfo?.discount?.[topupAmount] || DEFAULT_DISCOUNT_RATE

  return (
    <>
      <SectionPageLayout>
        <SectionPageLayout.Title>{t('Wallet')}</SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <RechargeHistoryButton
            open={
              props.onHistoryOpenChange
                ? props.initialShowHistory === true
                : undefined
            }
            defaultOpen={props.initialShowHistory}
            onOpenChange={props.onHistoryOpenChange}
          />
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <div className='mx-auto flex w-full max-w-[1600px] flex-col gap-3'>
            {walletError ? (
              <ErrorState
                className='min-h-[160px] rounded-lg border'
                onRetry={() => void refetchWallet()}
              />
            ) : (
              <WalletStatsCard
                user={user}
                summary={summary}
                userLoading={userLoading}
                summaryLoading={walletLoading}
              />
            )}
            <div className='grid items-stretch gap-4 lg:grid-cols-2'>
              <section aria-label={t('Add Funds')} className='min-w-0'>
                {recovery && (
                  <div
                    className='mb-3 rounded-md border p-3 text-sm'
                    role='status'
                  >
                    <p className='font-medium'>{t('Previous EPay order')}</p>
                    <p>
                      {(recovery.terms ?? recovery.draft).method} · ¥
                      {((recovery.terms ?? recovery.draft).minor / 100).toFixed(
                        2
                      )}
                    </p>
                    {recoveryStatus}
                    <Button
                      type='button'
                      variant='outline'
                      size='sm'
                      onClick={checkEpay}
                    >
                      {t('Check status')}
                    </Button>
                    {recovery.tradeNo && (
                      <Button
                        type='button'
                        variant='ghost'
                        size='sm'
                        onClick={forgetEpay}
                      >
                        {t('Dismiss reminder (does not cancel the order)')}
                      </Button>
                    )}
                  </div>
                )}
                <RechargeFormCard
                  usdtRecharge={
                    user?.id ? (
                      <UsdtRecharge
                        key={user.id}
                        userId={user.id}
                        onPaid={refreshBalances}
                      />
                    ) : null
                  }
                  topupInfo={topupInfo}
                  presetAmounts={presetAmounts}
                  selectedPreset={selectedPreset}
                  onSelectPreset={handleSelectPreset}
                  topupAmount={topupAmount}
                  onTopupAmountChange={handleTopupAmountChange}
                  paymentAmount={paymentAmount}
                  calculating={calculating}
                  onPaymentMethodSelect={handlePaymentMethodSelect}
                  paymentLoading={paymentLoading}
                  redemptionCode={redemptionCode}
                  onRedemptionCodeChange={setRedemptionCode}
                  onRedeem={handleRedeem}
                  redeeming={redeeming}
                  topupLink={topupInfo?.topup_link}
                  loading={topupLoading}
                  priceRatio={(status?.price as number) || 1}
                  usdExchangeRate={effectiveUsdExchangeRate}
                  creemProducts={topupInfo?.creem_products}
                  enableCreemTopup={topupInfo?.enable_creem_topup}
                  onCreemProductSelect={handleCreemProductSelect}
                  enableWaffoTopup={topupInfo?.enable_waffo_topup}
                  waffoPayMethods={topupInfo?.waffo_pay_methods}
                  waffoMinTopup={topupInfo?.waffo_min_topup}
                  onWaffoMethodSelect={handleWaffoMethodSelect}
                  enableWaffoPancakeTopup={
                    topupInfo?.enable_waffo_pancake_topup
                  }
                />
              </section>
              <section aria-label={t('Invitation Rewards')} className='min-w-0'>
                <ReferralPanel />
              </section>
            </div>
            <LedgerTable />
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <PaymentConfirmDialog
        orderRecovery={recoveryStatus}
        open={confirmDialogOpen}
        onOpenChange={(open) => {
          if (!open) invalidateEpayHandoff()
          setConfirmDialogOpen(open)
        }}
        onConfirm={handlePaymentConfirm}
        topupAmount={topupAmount}
        paymentAmount={paymentAmount}
        paymentMethod={selectedPaymentMethod}
        calculating={calculating}
        quoteValid={
          !!selectedPaymentMethod &&
          isQuoteCurrent(topupAmount, selectedPaymentMethod.type)
        }
        processing={processing || waffoProcessing || pancakeProcessing}
        discountRate={discountRate}
        usdExchangeRate={effectiveUsdExchangeRate}
      />

      <CreemConfirmDialog
        open={creemDialogOpen}
        onOpenChange={setCreemDialogOpen}
        onConfirm={handleCreemConfirm}
        product={selectedCreemProduct}
        processing={creemProcessing}
      />
    </>
  )
}
