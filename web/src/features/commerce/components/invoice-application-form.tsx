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
import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery } from '@tanstack/react-query'
import { useEffect, useRef, useState, type ReactNode } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Field, FieldError, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import { getInvoicePaymentOptions } from '../api'
import { invoiceOrderKey } from '../lib/invoice-orders'
import { invoiceSchema } from '../lib/schema'
import type { CommerceConfig, InvoiceInput, InvoiceOrder } from '../types'
import { InvoiceMoney } from './application-status'
import { InvoiceOrderSelector } from './invoice-order-selector'
import { InvoicePaymentMethods } from './invoice-payment-methods'

const EMPTY_ORDER_IDS: number[] = []

export type InvoicePaymentChoice = {
  method: string
  expectedWalletQuota?: string
}

export function InvoiceApplicationForm(props: {
  config: CommerceConfig
  orders: InvoiceOrder[]
  pending: boolean
  history?: ReactNode
  onSubmit: (
    input: InvoiceInput,
    requestKey: string,
    payment: InvoicePaymentChoice
  ) => Promise<void>
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const user = useAuthStore((state) => state.auth.user)
  const request = useRef({ signature: '', key: '' })
  const [method, setMethod] = useState('wallet')
  const form = useForm<InvoiceInput>({
    resolver: zodResolver(invoiceSchema),
    defaultValues: {
      order_ids: [],
      redemption_ids: [],
      category: 'technical_service',
      kind: 'general',
      title_type: 'company',
      title: '',
      tax_id: '',
      email: user?.email ?? '',
      address: '',
      phone: '',
      bank_name: '',
      bank_account: '',
    },
  })
  const selectedTopups = form.watch('order_ids')
  const selectedRedemptions = form.watch('redemption_ids') ?? EMPTY_ORDER_IDS
  const selectedOrders = props.orders.filter((order) =>
    order.source_type === 'redemption'
      ? selectedRedemptions.includes(order.redemption_id ?? 0)
      : selectedTopups.includes(order.topup_id)
  )
  const selectedKeys = selectedOrders.map(invoiceOrderKey)
  const kind = form.watch('kind')
  const titleType = form.watch('title_type')
  const category = form.watch('category')
  const company = titleType === 'company'
  const exponent = selectedOrders[0]?.currency_exponent ?? 2
  const currency = selectedOrders[0]?.currency ?? props.config.invoice_currency
  const consistentMoney = selectedOrders.every(
    (order) =>
      order.currency_exponent === exponent && order.currency === currency
  )
  const amount = consistentMoney
    ? selectedOrders.reduce(
        (sum, order) => sum + BigInt(order.amount_minor),
        0n
      )
    : 0n
  const feeBPS =
    kind === 'special'
      ? props.config.special_fee_bps
      : props.config.general_fee_bps
  const fee = (amount * BigInt(feeBPS) + 5000n) / 10000n
  const paymentOptions = useQuery({
    queryKey: [
      'commerce',
      'invoice-payment-quote',
      user?.id,
      currency,
      exponent,
      fee.toString(),
    ],
    queryFn: () =>
      getInvoicePaymentOptions({
        currency,
        exponent,
        feeMinor: fee.toString(),
      }),
    enabled: props.config.invoice_enabled && consistentMoney && fee > 0n,
    staleTime: 0,
  })
  const methods = paymentOptions.data?.methods ?? []
  const chosenMethod =
    methods.find((item) => item.type === method)?.type ?? methods[0]?.type ?? ''
  const insufficient =
    chosenMethod === 'wallet' &&
    paymentOptions.data &&
    BigInt(paymentOptions.data.wallet_balance_quota) <
      BigInt(paymentOptions.data.wallet_quota)
  const busy = props.pending || form.formState.isSubmitting
  const blocked =
    busy ||
    !props.config.invoice_enabled ||
    !selectedOrders.length ||
    !consistentMoney ||
    (fee > 0n &&
      (paymentOptions.isFetching ||
        paymentOptions.isError ||
        !chosenMethod ||
        insufficient))

  useEffect(() => {
    const topups = props.orders
      .filter(
        (order) =>
          order.source_type !== 'redemption' &&
          selectedTopups.includes(order.topup_id)
      )
      .map((order) => order.topup_id)
    const redemptions = props.orders
      .filter(
        (order) =>
          order.source_type === 'redemption' &&
          selectedRedemptions.includes(order.redemption_id ?? 0)
      )
      .map((order) => order.redemption_id as number)
    if (topups.length !== selectedTopups.length) {
      form.setValue('order_ids', topups)
    }
    if (redemptions.length !== selectedRedemptions.length) {
      form.setValue('redemption_ids', redemptions)
    }
  }, [props.orders, selectedTopups, selectedRedemptions, form])

  useEffect(() => {
    if (kind === 'special' && !props.config.special_invoice_enabled) {
      form.setValue('kind', 'general')
    }
  }, [kind, props.config.special_invoice_enabled, form])

  const submit = form.handleSubmit(async (values) => {
    if (blocked) return
    if (selectedOrders.length > props.config.invoice_max_orders) {
      form.setError('root', {
        message: t('Select up to {{count}} orders per application.', {
          count: props.config.invoice_max_orders,
        }),
      })
      return
    }
    if (!consistentMoney) {
      form.setError('root', {
        message: t('Select orders with the same currency precision'),
      })
      return
    }
    const data: InvoiceInput = {
      ...values,
      tax_id: company ? values.tax_id : '',
      address: kind === 'special' ? values.address : '',
      phone: kind === 'special' ? values.phone : '',
      bank_name: kind === 'special' ? values.bank_name : '',
      bank_account: kind === 'special' ? values.bank_account : '',
    }
    const signature = JSON.stringify(data)
    if (request.current.signature !== signature) {
      request.current = { signature, key: crypto.randomUUID() }
    }
    form.clearErrors('root')
    try {
      await props.onSubmit(data, request.current.key, {
        method: fee > 0n ? chosenMethod : '',
        expectedWalletQuota:
          chosenMethod === 'wallet'
            ? paymentOptions.data?.wallet_quota
            : undefined,
      })
      form.setValue('order_ids', [])
      form.setValue('redemption_ids', [])
      request.current = { signature: '', key: '' }
    } catch (error) {
      form.setError('root', { message: getServerErrorMessage(error) })
    }
  })
  const fields: {
    name:
      | 'title'
      | 'tax_id'
      | 'address'
      | 'phone'
      | 'bank_name'
      | 'bank_account'
    label: string
    maxLength: number
  }[] = [
    { name: 'title', label: t('Invoice title'), maxLength: 200 },
    ...(company
      ? [
          {
            name: 'tax_id' as const,
            label: t('Tax identification number'),
            maxLength: 64,
          },
        ]
      : []),
    ...(kind === 'special'
      ? [
          {
            name: 'address' as const,
            label: t('Registered address'),
            maxLength: 300,
          },
          {
            name: 'phone' as const,
            label: t('Registered phone'),
            maxLength: 64,
          },
          { name: 'bank_name' as const, label: t('Bank name'), maxLength: 200 },
          {
            name: 'bank_account' as const,
            label: t('Bank account'),
            maxLength: 80,
          },
        ]
      : []),
  ]
  return (
    <div className='grid items-start gap-4 lg:grid-cols-[minmax(0,1.7fr)_minmax(330px,1fr)]'>
      <div className='min-w-0 space-y-4'>
        <InvoiceOrderSelector
          config={props.config}
          orders={props.orders}
          selected={selectedKeys}
          disabled={busy || !props.config.invoice_enabled}
          onChange={(keys) => {
            form.setValue(
              'order_ids',
              props.orders
                .filter(
                  (order) =>
                    order.source_type !== 'redemption' &&
                    keys.includes(invoiceOrderKey(order))
                )
                .map((order) => order.topup_id),
              { shouldValidate: true }
            )
            form.setValue(
              'redemption_ids',
              props.orders
                .filter(
                  (order) =>
                    order.source_type === 'redemption' &&
                    keys.includes(invoiceOrderKey(order))
                )
                .map((order) => order.redemption_id as number),
              { shouldValidate: true }
            )
          }}
        />
        {props.history}
      </div>
      <Card className='min-w-0 rounded-3xl'>
        <CardHeader>
          <CardTitle>{t('Invoice information')}</CardTitle>
          <p className='text-muted-foreground text-sm'>
            {t('{{count}} orders selected', { count: selectedOrders.length })}
          </p>
        </CardHeader>
        <CardContent>
          <form onSubmit={submit} noValidate className='grid gap-4'>
            <Field>
              <FieldLabel>{t('Invoice type')}</FieldLabel>
              <ToggleGroup
                value={[kind]}
                onValueChange={(values) => {
                  const next = values[0]
                  if (next === 'general' || next === 'special') {
                    form.setValue('kind', next)
                    if (next === 'special') {
                      form.setValue('title_type', 'company')
                    }
                  }
                }}
                aria-label={t('Invoice type')}
                disabled={busy}
                variant='outline'
                spacing={2}
                className='grid w-full grid-cols-2'
              >
                <ToggleGroupItem
                  value='general'
                  className='rounded-full text-xs'
                >
                  {t('VAT general invoice')}
                </ToggleGroupItem>
                <ToggleGroupItem
                  value='special'
                  disabled={!props.config.special_invoice_enabled}
                  className='rounded-full text-xs'
                >
                  {t('VAT special invoice')}
                </ToggleGroupItem>
              </ToggleGroup>
            </Field>
            <Field>
              <FieldLabel>{t('Title type')}</FieldLabel>
              <ToggleGroup
                value={[titleType]}
                onValueChange={(values) => {
                  if (values[0] === 'company' || values[0] === 'individual') {
                    form.setValue('title_type', values[0])
                    form.clearErrors('tax_id')
                  }
                }}
                aria-label={t('Title type')}
                disabled={busy}
                variant='outline'
                spacing={2}
                className='grid w-full grid-cols-2'
              >
                <ToggleGroupItem value='company' className='rounded-full'>
                  {t('Company')}
                </ToggleGroupItem>
                <ToggleGroupItem
                  value='individual'
                  disabled={kind === 'special'}
                  className='rounded-full'
                >
                  {t('Individual')}
                </ToggleGroupItem>
              </ToggleGroup>
            </Field>
            {fields.map((field) => (
              <Field
                key={field.name}
                data-invalid={!!form.formState.errors[field.name]}
              >
                <FieldLabel htmlFor={`invoice-${field.name}`}>
                  {field.label}
                  <span aria-hidden className='text-destructive'>
                    *
                  </span>
                </FieldLabel>
                <Input
                  id={`invoice-${field.name}`}
                  aria-label={field.label}
                  className='rounded-full'
                  {...form.register(field.name)}
                  maxLength={field.maxLength}
                  disabled={busy}
                  required
                  aria-invalid={!!form.formState.errors[field.name]}
                  aria-describedby={
                    form.formState.errors[field.name]
                      ? `invoice-error-${field.name}`
                      : undefined
                  }
                />
                <FieldError id={`invoice-error-${field.name}`}>
                  {form.formState.errors[field.name]?.message &&
                    t(form.formState.errors[field.name]?.message ?? '')}
                </FieldError>
              </Field>
            ))}
            <Field>
              <FieldLabel htmlFor='invoice-category'>
                {t('Invoice category')}
                <span aria-hidden className='text-destructive'>
                  *
                </span>
              </FieldLabel>
              <Select
                value={category}
                disabled={busy}
                onValueChange={(value) => {
                  if (
                    value === 'technical_service' ||
                    value === 'software_development'
                  ) {
                    form.setValue('category', value)
                  }
                }}
              >
                <SelectTrigger
                  id='invoice-category'
                  className='w-full rounded-full'
                  aria-label={t('Invoice category')}
                >
                  <SelectValue>
                    {category === 'software_development'
                      ? t('Software development services')
                      : t('Technical service fee')}
                  </SelectValue>
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value='technical_service'>
                    {t('Technical service fee')}
                  </SelectItem>
                  <SelectItem value='software_development'>
                    {t('Software development services')}
                  </SelectItem>
                </SelectContent>
              </Select>
            </Field>
            <Field data-invalid={!!form.formState.errors.email}>
              <FieldLabel htmlFor='invoice-email'>
                {t('Delivery email')}
                <span aria-hidden className='text-destructive'>
                  *
                </span>
              </FieldLabel>
              <Input
                id='invoice-email'
                type='email'
                aria-label={t('Delivery email')}
                className='rounded-full'
                {...form.register('email')}
                maxLength={254}
                disabled={busy}
                required
                aria-invalid={!!form.formState.errors.email}
                aria-describedby={
                  form.formState.errors.email
                    ? 'invoice-error-email'
                    : undefined
                }
              />
              <FieldError id='invoice-error-email'>
                {form.formState.errors.email?.message &&
                  t(form.formState.errors.email.message)}
              </FieldError>
            </Field>
            <dl className='grid gap-2 border-t pt-4 text-sm'>
              <div className='flex justify-between gap-3'>
                <dt className='text-muted-foreground'>{t('Invoice amount')}</dt>
                <dd>
                  <InvoiceMoney
                    minor={amount.toString()}
                    currency={currency}
                    exponent={exponent}
                  />
                </dd>
              </div>
              <div className='flex justify-between gap-3'>
                <dt className='text-muted-foreground'>
                  {t('Tax fee ({{rate}}%)', {
                    rate: formatNumber(feeBPS / 100, locale),
                  })}
                </dt>
                <dd>
                  <InvoiceMoney
                    minor={fee.toString()}
                    currency={currency}
                    exponent={exponent}
                  />
                </dd>
              </div>
              <div className='flex justify-between gap-3 font-semibold'>
                <dt>{t('Payment due')}</dt>
                <dd>
                  <InvoiceMoney
                    minor={fee.toString()}
                    currency={currency}
                    exponent={exponent}
                  />
                </dd>
              </div>
            </dl>
            {fee > 0n && paymentOptions.isPending && (
              <LoadingState message={t('Loading payment methods...')} />
            )}
            {fee > 0n && paymentOptions.isError && (
              <ErrorState onRetry={() => void paymentOptions.refetch()} />
            )}
            {fee > 0n && paymentOptions.data && (
              <InvoicePaymentMethods
                options={paymentOptions.data}
                method={chosenMethod}
                disabled={busy || paymentOptions.isFetching}
                onChange={setMethod}
              />
            )}
            {fee > 0n && paymentOptions.data && !methods.length && (
              <p className='text-muted-foreground text-xs'>
                {t('No compatible online payment method is configured.')}
              </p>
            )}
            {form.formState.errors.order_ids?.message && (
              <p role='alert' className='text-destructive text-sm'>
                {t(form.formState.errors.order_ids.message)}
              </p>
            )}
            {!consistentMoney && (
              <p role='alert' className='text-destructive text-sm'>
                {t('Select orders with the same currency precision')}
              </p>
            )}
            {form.formState.errors.root?.message && (
              <p role='alert' className='text-destructive text-sm'>
                {form.formState.errors.root.message}
              </p>
            )}
            <Button
              type='submit'
              className='w-full rounded-full'
              disabled={!!blocked}
            >
              {busy
                ? t('Submitting...')
                : t(fee > 0n ? 'Submit and pay tax fee' : 'Submit application')}
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  )
}
