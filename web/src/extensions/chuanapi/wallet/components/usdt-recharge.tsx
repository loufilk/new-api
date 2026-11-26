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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Coins, Loader2 } from 'lucide-react'
import { QRCodeSVG } from 'qrcode.react'
import { useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'
import { toIntlLocale } from '@/i18n/languages'

import {
  liveUsdtOrder,
  loadUsdtDraft,
  lookupUsdtRequest,
  payableUsdtOrder,
  postUsdtOrder,
  saveUsdtDraft,
  usdtOptions,
  usdtOrder,
  usdtOrders,
  UsdtTermsRejected,
  validUsdtAddress,
  withinCnyLimits,
  type UsdtDraft,
  type UsdtNetwork,
  type UsdtOrder,
} from '../usdt-api'

const querySettings = { retry: false, meta: { errorToast: false } }
const networkNames: Record<UsdtNetwork, string> = {
  bsc: 'BSC (BEP-20)',
  polygon: 'Polygon',
  tron: 'TRON (TRC-20)',
}

export function UsdtRecharge({
  userId,
  onPaid,
}: {
  userId: number
  onPaid: () => void
}) {
  const { t, i18n } = useTranslation()
  const client = useQueryClient()
  const htmlId = useId()
  const [open, setOpen] = useState(false)
  const [attempt, setAttempt] = useState(() => loadUsdtDraft(userId))
  const [network, setNetwork] = useState<UsdtNetwork | ''>(
    attempt?.network ?? ''
  )
  const [amount, setAmount] = useState(attempt?.amount ?? '')
  const [confirmed, setConfirmed] = useState('')
  const [creating, setCreating] = useState(false)
  const [termsChanged, setTermsChanged] = useState(false)
  const [reviewing, setReviewing] = useState(false)
  const [selection, setSelection] = useState<string | null>(null)
  const [now, setNow] = useState(Date.now)
  const lock = useRef(false)
  const unpaid = useRef(new Set<string>())
  const paid = useRef(new Set<string>())
  const { copyToClipboard } = useCopyToClipboard()
  const optionsKey = ['wallet-usdt', userId, 'options']
  const ordersKey = ['wallet-usdt', userId, 'orders']
  const options = useQuery({
    ...querySettings,
    queryKey: optionsKey,
    queryFn: ({ signal }) => usdtOptions(signal),
    refetchOnMount: 'always',
  })
  const ready = options.isFetchedAfterMount && options.isSuccess
  const networks = options.data?.networks ?? []
  const enabled = ready && !!options.data?.enabled && networks.length > 0
  const orders = useQuery({
    ...querySettings,
    queryKey: ordersKey,
    queryFn: ({ signal }) => usdtOrders(signal),
    // The order list is account-scoped, even when configuration is unavailable.
    enabled: options.isFetchedAfterMount,
    refetchOnMount: 'always',
    refetchInterval: (query) =>
      open && query.state.data?.some(liveUsdtOrder) ? 15_000 : false,
  })
  const history = orders.data ?? []
  const id =
    selection === 'new'
      ? undefined
      : (selection ?? (attempt ? undefined : history.find(liveUsdtOrder)?.id))
  const detail = useQuery({
    ...querySettings,
    queryKey: ['wallet-usdt', userId, 'order', id],
    queryFn: ({ signal }) => {
      if (!id) throw new Error('No selected order')
      return usdtOrder(id, signal)
    },
    enabled: open && !!id,
    refetchInterval: (query) =>
      query.state.data && liveUsdtOrder(query.state.data) ? 5_000 : false,
  })
  const order = detail.data ?? history.find((item) => item.id === id)
  const expiry = order?.expires_at
  useEffect(() => {
    if (!open || !expiry) return
    const remaining = Date.parse(expiry) - Date.now()
    if (!Number.isFinite(remaining) || remaining > 2_147_483_647) return
    const timer = setTimeout(() => setNow(Date.now()), Math.max(0, remaining))
    return () => clearTimeout(timer)
  }, [open, expiry])

  useEffect(() => {
    let credited = false
    for (const item of [
      ...(orders.data ?? []),
      ...(detail.data ? [detail.data] : []),
    ]) {
      if (liveUsdtOrder(item) && !paid.current.has(item.id)) {
        unpaid.current.add(item.id)
      }
      if (item.status === 'paid') {
        if (unpaid.current.delete(item.id)) credited = true
        paid.current.add(item.id)
      }
    }
    if (credited) {
      void client.invalidateQueries({
        queryKey: ['wallet-usdt', userId, 'orders'],
      })
      void client.invalidateQueries({ queryKey: ['wallet', 'ledger'] })
      onPaid()
    }
  }, [detail.data, orders.data, client, userId, onPaid])

  const requestedAmount = amount.trim().replace(',', '.')
  const validAmount = withinCnyLimits(
    requestedAmount,
    options.data?.min_amount,
    options.data?.max_amount
  )
  const selectedNetwork = networks.find((item) => item.id === network)
  const createAllowed =
    enabled &&
    !options.isFetching &&
    !!selectedNetwork &&
    validAmount &&
    !creating &&
    orders.isSuccess
  const canPay = order && detail.isSuccess && payableUsdtOrder(order, now)
  const address =
    canPay && validUsdtAddress(order.network, order.address)
      ? order.address
      : undefined
  const quote = order
    ? JSON.stringify([
        order.id,
        order.network,
        order.usdt_amount,
        expiry,
        address,
      ])
    : ''

  async function submitOrder() {
    if (lock.current || (!attempt && !createAllowed)) return
    lock.current = true
    setCreating(true)
    try {
      let result: UsdtOrder
      if (attempt) {
        // Recovery never creates an order, including after a 404 or a reload.
        result = await lookupUsdtRequest(attempt)
      } else {
        if (!selectedNetwork) return
        const draft: UsdtDraft = {
          network: selectedNetwork.id,
          amount: requestedAmount,
          idempotency_key: crypto.randomUUID(),
        }
        saveUsdtDraft(userId, draft)
        setAttempt(draft)
        setTermsChanged(false)
        result = await postUsdtOrder(draft)
      }
      saveUsdtDraft(userId)
      setAttempt(undefined)
      setSelection(result.id)
      setReviewing(false)
      client.setQueryData<UsdtOrder[]>(ordersKey, (old) => [
        result,
        ...(old ?? []).filter((item) => item.id !== result.id),
      ])
      if (result.status === 'paid' && !paid.current.has(result.id)) {
        paid.current.add(result.id)
        unpaid.current.delete(result.id)
        void client.invalidateQueries({ queryKey: ['wallet', 'ledger'] })
        onPaid()
      }
    } catch (error) {
      if (!attempt && error instanceof UsdtTermsRejected) {
        saveUsdtDraft(userId)
        setAttempt(undefined)
        setNetwork('')
        setReviewing(false)
        setTermsChanged(true)
        await options.refetch()
      } else {
        // Unknown/duplicate outcomes keep the original receipt for lookup.
        void client.invalidateQueries({ queryKey: ordersKey })
      }
    } finally {
      lock.current = false
      setCreating(false)
    }
  }

  function statusLabel(status: UsdtOrder['status']) {
    switch (status) {
      case 'creating':
        return t('Preparing USDT order')
      case 'pending':
        return t('Awaiting transfer')
      case 'confirming':
        return t('Confirming on chain')
      case 'paid':
        return t('Credited')
      case 'expired':
        return t('Expired')
      case 'review':
        return t('Manual review')
      case 'failed':
        return t('Failed')
    }
  }

  if (!enabled && history.length === 0 && !attempt && !(open && termsChanged)) {
    return null
  }

  return (
    <section
      aria-label={t('USDT wallet recharge')}
      className='space-y-2 border-t pt-3'
    >
      <Button
        type='button'
        variant='outline'
        className='min-h-10 w-full sm:w-auto'
        onClick={() => {
          setNow(Date.now())
          setConfirmed('')
          setReviewing(false)
          setSelection(null)
          setOpen(true)
        }}
      >
        <Coins aria-hidden='true' />{' '}
        {enabled ? t('Pay with USDT') : t('USDT orders')}
      </Button>
      <Dialog
        open={open}
        onOpenChange={(next) => {
          if (!creating || next) setOpen(next)
        }}
      >
        <DialogContent className='min-w-0 sm:max-w-xl'>
          <DialogHeader>
            <DialogTitle>{t('USDT wallet recharge')}</DialogTitle>
            <DialogDescription>
              {t('Only transfer USDT on the network shown in the order.')}
            </DialogDescription>
          </DialogHeader>
          {termsChanged && !order && (
            <Alert>
              <AlertDescription>
                {t(
                  'Recharge options changed. Review the current network and amount before creating an order.'
                )}
              </AlertDescription>
            </Alert>
          )}
          {order && (
            <div className='min-w-0 space-y-3'>
              <p aria-live='polite' className='font-medium'>
                {statusLabel(order.status)}
              </p>
              <dl className='bg-muted/40 grid min-w-0 grid-cols-[minmax(0,1fr)_minmax(0,1fr)] gap-2 rounded-lg border p-3 text-sm [&>dd]:text-right [&>dd]:break-all'>
                <dt>{t('Payment network')}</dt>
                <dd>{networkNames[order.network]}</dd>
                <dt>{t('Exact amount to send')}</dt>
                <dd className='font-semibold tabular-nums'>
                  {order.usdt_amount
                    ? `${order.usdt_amount} USDT`
                    : t('Not available')}
                </dd>
                <dt>{t('Recharge principal (CNY)')}</dt>
                <dd>CNY {order.amount}</dd>
                <dt>{t('Wallet credit (principal)')}</dt>
                <dd>
                  {order.wallet_credit
                    ? t('{{amount}} wallet units', {
                        amount: order.wallet_credit,
                      })
                    : t('Not available')}
                </dd>
                <dt>{t('Separate bonus credit')}</dt>
                <dd>
                  {order.bonus_credit
                    ? t('{{amount}} wallet units', {
                        amount: order.bonus_credit,
                      })
                    : t('Not available')}
                </dd>
                {expiry && Number.isFinite(Date.parse(expiry)) ? (
                  <>
                    <dt>{t('Order expires at')}</dt>
                    <dd>
                      <time dateTime={expiry}>
                        {new Date(expiry).toLocaleString(
                          toIntlLocale(i18n.resolvedLanguage || i18n.language)
                        )}
                      </time>
                    </dd>
                  </>
                ) : null}
                <dt>{t('Order ID')}</dt>
                <dd className='font-mono'>{order.id}</dd>
                {order.tx_id ? (
                  <>
                    <dt>{t('Transaction ID')}</dt>
                    <dd className='font-mono'>{order.tx_id}</dd>
                  </>
                ) : null}
              </dl>
              {detail.isError && (
                <Alert variant='destructive'>
                  <AlertDescription>
                    {t(
                      'The order could not be refreshed. Check its status before paying.'
                    )}
                  </AlertDescription>
                </Alert>
              )}
              {order.status === 'review' && (
                <Alert>
                  <AlertDescription>
                    {t('Manual review required. Do not transfer again.')}
                  </AlertDescription>
                </Alert>
              )}
              {(order.status === 'expired' ||
                (order.status === 'pending' &&
                  Date.parse(expiry ?? '') <= now)) && (
                <Alert>
                  <AlertDescription>
                    {t(
                      'This quote has expired. Do not send funds to the old address.'
                    )}
                  </AlertDescription>
                </Alert>
              )}
              {canPay && address ? (
                <div className='space-y-2 rounded-lg border p-3'>
                  <div className='flex items-start gap-2'>
                    <Checkbox
                      id={`${htmlId}-verify`}
                      aria-labelledby={`${htmlId}-verify-label`}
                      checked={confirmed === quote}
                      onCheckedChange={(checked) =>
                        setConfirmed(checked ? quote : '')
                      }
                    />
                    <Label
                      id={`${htmlId}-verify-label`}
                      htmlFor={`${htmlId}-verify`}
                      className='leading-snug'
                    >
                      {t('I checked the network and the exact USDT amount.')}
                    </Label>
                  </div>
                  {confirmed === quote && address && (
                    <>
                      <p className='text-muted-foreground text-xs'>
                        {t('Receiving address')}
                      </p>
                      <div className='w-fit rounded-md border bg-white p-2'>
                        <QRCodeSVG
                          value={address}
                          size={128}
                          marginSize={2}
                          role='img'
                          title={t('USDT receiving address QR code')}
                        />
                      </div>
                      <p className='font-mono text-xs break-all select-all'>
                        {address}
                      </p>
                      <Button
                        variant='outline'
                        size='sm'
                        onClick={() => {
                          if (payableUsdtOrder(order)) {
                            void copyToClipboard(address)
                          }
                        }}
                      >
                        {t('Copy address')}
                      </Button>
                    </>
                  )}
                  <p className='text-muted-foreground text-xs'>
                    {t(
                      'Transfer the exact amount on the selected network before the order expires. Do not switch networks or renew this quote.'
                    )}
                  </p>
                </div>
              ) : (
                canPay && (
                  <Alert>
                    <AlertDescription>
                      {t('No verified payment destination. Do not send funds.')}
                    </AlertDescription>
                  </Alert>
                )
              )}
              <div className='flex flex-wrap gap-2'>
                <Button
                  variant='outline'
                  onClick={() => void detail.refetch()}
                  disabled={detail.isFetching}
                >
                  {t('Refresh status')}
                </Button>
                {(enabled || attempt) && (
                  <Button
                    variant='outline'
                    onClick={() => {
                      setSelection('new')
                      setConfirmed('')
                      setReviewing(false)
                    }}
                  >
                    {attempt
                      ? t('Return to pending order request')
                      : t('Create a different order')}
                  </Button>
                )}
              </div>
            </div>
          )}
          {!order && attempt && (
            <div className='space-y-3'>
              <Alert>
                <AlertDescription>
                  {t(
                    'Order not confirmed yet. Check this request or contact support with its reference. Do not transfer again.'
                  )}
                </AlertDescription>
              </Alert>
              <p className='text-sm'>
                {networkNames[attempt.network]} · CNY {attempt.amount}
              </p>
              <p className='font-mono text-xs break-all'>
                {attempt.idempotency_key}
              </p>
              <Button disabled={creating} onClick={() => void submitOrder()}>
                {creating && (
                  <Loader2 className='size-4 animate-spin' aria-hidden='true' />
                )}
                {t('Check order status')}
              </Button>
            </div>
          )}
          {!order && !attempt && enabled && (
            <div className='space-y-3'>
              {orders.isError && (
                <Alert variant='destructive'>
                  <AlertDescription>
                    {t(
                      'Could not load USDT orders. Refresh before creating an order.'
                    )}
                  </AlertDescription>
                </Alert>
              )}
              <div className='grid gap-3 sm:grid-cols-2'>
                <div className='space-y-1.5'>
                  <Label htmlFor={`${htmlId}-network`}>
                    {t('Payment network')}
                  </Label>
                  <NativeSelect
                    id={`${htmlId}-network`}
                    className='w-full'
                    value={selectedNetwork?.id ?? ''}
                    disabled={creating || reviewing}
                    onChange={(event) =>
                      setNetwork(
                        networks.find((item) => item.id === event.target.value)
                          ?.id ?? ''
                      )
                    }
                  >
                    <NativeSelectOption value=''>
                      {t('Select a network')}
                    </NativeSelectOption>
                    {networks.map((item) => (
                      <NativeSelectOption key={item.id} value={item.id}>
                        {networkNames[item.id]}
                      </NativeSelectOption>
                    ))}
                  </NativeSelect>
                  {selectedNetwork && selectedNetwork.bonus_bps > 0 && (
                    <p className='text-muted-foreground text-xs'>
                      {t('Wallet bonus: {{percent}}', {
                        percent: new Intl.NumberFormat(
                          toIntlLocale(i18n.resolvedLanguage || i18n.language),
                          { style: 'percent', maximumFractionDigits: 2 }
                        ).format(selectedNetwork.bonus_bps / 10000),
                      })}
                    </p>
                  )}
                </div>
                <div className='space-y-1.5'>
                  <Label htmlFor={`${htmlId}-amount`}>
                    {t('Recharge principal (CNY)')}
                  </Label>
                  <Input
                    id={`${htmlId}-amount`}
                    inputMode='decimal'
                    value={amount}
                    maxLength={15}
                    aria-invalid={!!amount && !validAmount}
                    aria-describedby={`${htmlId}-range`}
                    disabled={creating || reviewing}
                    onChange={(event) => setAmount(event.target.value)}
                  />
                  <p
                    id={`${htmlId}-range`}
                    className='text-muted-foreground text-xs'
                  >
                    {t('CNY {{min}} to {{max}}; up to two decimal places.', {
                      min: options.data?.min_amount,
                      max: options.data?.max_amount,
                    })}
                  </p>
                </div>
              </div>
              {reviewing ? (
                <>
                  <Alert>
                    <AlertDescription>
                      {t(
                        'This only creates an order. No transfer is sent automatically.'
                      )}
                    </AlertDescription>
                  </Alert>
                  <div className='grid gap-2 sm:flex sm:justify-end'>
                    <Button
                      variant='outline'
                      disabled={creating}
                      onClick={() => setReviewing(false)}
                    >
                      {t('Back')}
                    </Button>
                    <Button
                      disabled={!createAllowed}
                      onClick={() => void submitOrder()}
                    >
                      {creating && (
                        <Loader2
                          className='size-4 animate-spin'
                          aria-hidden='true'
                        />
                      )}
                      {t('Confirm and create order')}
                    </Button>
                  </div>
                </>
              ) : (
                <Button
                  className='w-full sm:w-auto'
                  disabled={!createAllowed}
                  onClick={() => setReviewing(true)}
                >
                  {t('Review USDT order')}
                </Button>
              )}
            </div>
          )}
          <section
            aria-label={t('USDT order history')}
            className='space-y-2 border-t pt-3'
          >
            <div className='flex justify-between gap-2'>
              <h3 className='text-sm font-medium'>{t('USDT order history')}</h3>
              <Button
                size='sm'
                variant='ghost'
                onClick={() => void orders.refetch()}
                disabled={orders.isFetching}
              >
                {t('Refresh')}
              </Button>
            </div>
            {orders.isLoading && <p role='status'>{t('Loading...')}</p>}
            {orders.isSuccess && history.length === 0 && !attempt && (
              <p className='text-muted-foreground text-sm'>
                {t('No USDT recharge orders yet.')}
              </p>
            )}
            {history.length > 0 && (
              <ul className='max-h-48 space-y-1 overflow-y-auto overscroll-contain'>
                {history.map((item) => (
                  <li key={item.id} className='min-w-0'>
                    <Button
                      type='button'
                      variant={id === item.id ? 'secondary' : 'ghost'}
                      aria-pressed={id === item.id}
                      className='h-auto min-h-9 w-full justify-between gap-2 px-2 py-1.5 text-start whitespace-normal'
                      onClick={() => {
                        setSelection(item.id)
                        setConfirmed('')
                        setReviewing(false)
                      }}
                    >
                      <span className='min-w-0 break-words'>
                        {networkNames[item.network]} · CNY {item.amount}
                      </span>
                      <span className='shrink-0 text-xs'>
                        {statusLabel(item.status)}
                      </span>
                    </Button>
                  </li>
                ))}
              </ul>
            )}
          </section>
        </DialogContent>
      </Dialog>
    </section>
  )
}
