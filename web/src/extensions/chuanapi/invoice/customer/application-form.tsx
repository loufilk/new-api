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
import { useMutation } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Card, CardHeader, CardTitle, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { useRequestId } from '@/features/agent-platform/hooks/use-request-id'
import {
  getServerErrorMessage,
  getServerErrorStatus,
} from '@/lib/server-error-message'

import { invoiceCategoryOptionLabel } from '../categories'
import { formatInvoiceRate } from '../contracts'
import { invoiceAPI } from './api'
import { useInvoiceText, type InvoiceTextKey } from './copy'
import {
  invoiceSchema,
  type InvoiceFormInput,
  type InvoiceFields,
} from './form-schema'
import type { OrderSelection } from './orders'
import { estimateInvoiceFee, type CustomerInvoiceConfig } from './policy'

export function ApplicationForm(props: {
  policyReady?: boolean
  policy: CustomerInvoiceConfig
  selection: OrderSelection
  onBusy: (busy: boolean) => void
  onSuccess: (paymentId?: number) => void
  onConflict: () => void
}) {
  const { text, money, locale } = useInvoiceText()
  const categories = props.policy.categories
  const firstCategory = categories[0]
  const defaults: InvoiceFormInput = {
    invoice_type: props.policy.ordinary.enabled ? 'ordinary' : 'special',
    title_type: 'company',
    category: firstCategory?.id ?? '',
    title: '',
    tax_id: '',
    email: '',
  }
  const form = useForm<InvoiceFormInput, unknown, InvoiceFields>({
    resolver: zodResolver(invoiceSchema),
    defaultValues: defaults,
  })
  const [reviewedName, setReviewedName] = useState<string | undefined>(
    firstCategory?.name
  )
  const categoryId = useWatch({ control: form.control, name: 'category' })
  const selectedCategory = categories.find(
    (item) => item.id === categoryId && item.name === reviewedName
  )
  // Do not let the native select silently pick the next item after a policy
  // refresh. A removed or renamed choice needs an explicit new selection.
  useEffect(() => {
    if (categoryId && !selectedCategory) form.setValue('category', '')
  }, [categoryId, selectedCategory, form])
  const type = useWatch({
    control: form.control,
    name: 'invoice_type',
  })
  const identify = useRequestId()
  const mutation = useMutation({
    gcTime: 0,
    mutationFn: (request: Parameters<typeof invoiceAPI.submit>) =>
      invoiceAPI.submit(...request),
    onSuccess: (application) => {
      setReviewedName(firstCategory?.name)
      form.reset(defaults)
      identify.reset()
      props.onSuccess(
        application.status === 'awaiting_payment' ? application.id : undefined
      )
      toast.success(text('Application submitted.'))
    },
    meta: { errorToast: false },
    onError: (error) => {
      const status = getServerErrorStatus(error)
      if (status === 409 || status === 403) props.onConflict()
    },
    onSettled: () => props.onBusy(false),
  })
  const special = type === 'special'
  const total = [...props.selection.values()].reduce(
    (sum, amount) => sum + BigInt(amount),
    0n
  )
  const typePolicy = props.policy[type]
  const fee = estimateInvoiceFee(total, typePolicy)
  const paymentAvailable =
    fee <= BigInt(Number.MAX_SAFE_INTEGER) &&
    props.policy.fee_payment_methods.some(
      (method) =>
        method.rail === 'epay' ||
        (props.policy.wallet_quota_per_cent > 0 &&
          fee * BigInt(props.policy.wallet_quota_per_cent) <=
            BigInt(Number.MAX_SAFE_INTEGER))
    )
  const paymentBlocked = fee > 0n && !paymentAvailable
  let submitLabel: InvoiceTextKey =
    fee > 0n ? 'Submit and choose payment' : 'Submit application'
  if (mutation.isPending) submitLabel = 'Submitting…'
  const disabled =
    props.policyReady === false ||
    !props.selection.size ||
    !props.policy.enabled ||
    !typePolicy.enabled ||
    !selectedCategory ||
    paymentBlocked ||
    mutation.isPending
  const field = (
    name:
      | 'title'
      | 'email'
      | 'tax_id'
      | 'address'
      | 'phone'
      | 'bank_name'
      | 'bank_account',
    label: InvoiceTextKey,
    inputType: 'text' | 'tel' | 'email' = 'text'
  ) => {
    const message = form.formState.errors[name]?.message
    const describedBy =
      [
        name === 'email' && 'invoice-email-note',
        message && `invoice-error-${name}`,
      ]
        .filter(Boolean)
        .join(' ') || undefined
    return (
      <div className='space-y-1.5'>
        <Label htmlFor={`invoice-${name}`}>{text(label)}</Label>
        <Input
          id={`invoice-${name}`}
          {...form.register(name)}
          type={inputType}
          autoComplete='off'
          aria-required='true'
          aria-invalid={!!message}
          aria-describedby={describedBy}
        />
        {name === 'email' && (
          <p id='invoice-email-note' className='text-muted-foreground text-xs'>
            {text(
              'For contact only. Invoices are not emailed automatically. Download issued invoices from My applications.'
            )}
          </p>
        )}
        {message && (
          <p
            role='alert'
            id={`invoice-error-${name}`}
            className='text-destructive text-xs'
          >
            {text(message as InvoiceTextKey)}
          </p>
        )}
      </div>
    )
  }
  return (
    <Card className='min-w-0'>
      <CardHeader>
        <CardTitle>{text('Invoice information')}</CardTitle>
      </CardHeader>
      <CardContent>
        <form
          noValidate
          onSubmit={form.handleSubmit((values) => {
            if (
              disabled ||
              values.invoice_type !== type ||
              values.category !== selectedCategory?.id
            ) {
              return
            }
            const orderIds = [...props.selection.keys()].sort((a, b) => a - b)
            const version = props.policy.version
            props.onBusy(true)
            mutation.mutate([
              values,
              orderIds,
              identify([values, orderIds, version]),
              version,
            ])
          })}
        >
          <fieldset className='space-y-4' disabled={mutation.isPending}>
            <div className='space-y-1.5'>
              <Label htmlFor='invoice-type'>{text('Invoice type')}</Label>
              <NativeSelect
                className='w-full'
                id='invoice-type'
                {...form.register('invoice_type')}
              >
                <NativeSelectOption
                  value='ordinary'
                  disabled={!props.policy.ordinary.enabled}
                >
                  {text('Ordinary invoice')}
                </NativeSelectOption>
                <NativeSelectOption
                  value='special'
                  disabled={!props.policy.special.enabled}
                >
                  {text('Special VAT invoice')}
                </NativeSelectOption>
              </NativeSelect>
            </div>
            <p className='text-muted-foreground text-xs'>
              {text(
                'Only company invoices are available. Enter the registered company name and tax ID.'
              )}
            </p>
            {field('title', 'Invoice title')}
            {field('tax_id', 'Tax ID')}
            <div className='space-y-1.5'>
              <Label htmlFor='invoice-category'>
                {text('Invoice category')}
              </Label>
              <NativeSelect
                id='invoice-category'
                className='w-full'
                value={selectedCategory?.id ?? ''}
                required
                {...form.register('category', {
                  onChange: (event: React.ChangeEvent<HTMLSelectElement>) => {
                    setReviewedName(
                      categories.find((item) => item.id === event.target.value)
                        ?.name
                    )
                  },
                })}
              >
                <NativeSelectOption value='' disabled>
                  {text('Select an invoice category')}
                </NativeSelectOption>
                {categories.map((item) => (
                  <NativeSelectOption key={item.id} value={item.id}>
                    {invoiceCategoryOptionLabel(item, (key) => text(key))}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </div>
            {special && (
              <>
                {field('address', 'Registered address')}
                {field('phone', 'Registered phone', 'tel')}
                {field('bank_name', 'Bank name')}
                {field('bank_account', 'Bank account number')}
              </>
            )}
            {field('email', 'Email address', 'email')}
            <dl
              aria-live='polite'
              className='bg-muted/50 space-y-2 rounded-lg p-3'
            >
              <div className='flex justify-between gap-2'>
                <dt className='text-muted-foreground'>
                  {text('Selected orders')}
                </dt>
                <dd>{text('{{n}} orders', { n: props.selection.size })}</dd>
              </div>
              <div className='flex justify-between gap-2'>
                <dt>{text('Invoice amount')}</dt>
                <dd className='font-semibold tabular-nums'>
                  {money(total.toString())}
                </dd>
              </div>
              <div className='flex justify-between gap-2'>
                <dt>{text('Estimated invoice fee')}</dt>
                <dd className='tabular-nums'>
                  {money(fee.toString())} (
                  {formatInvoiceRate(
                    typePolicy.fee_enabled ? typePolicy.fee_rate_bps : 0,
                    locale
                  )}
                  )
                </dd>
              </div>
              {props.policy.version != null && props.policy.version > 0 && (
                <div className='flex justify-between gap-2'>
                  <dt>{text('Configured tax rate (reference only)')}</dt>
                  <dd className='tabular-nums'>
                    {formatInvoiceRate(typePolicy.tax_rate_bps, locale)}
                  </dd>
                </div>
              )}
            </dl>
            {fee > 0n && paymentAvailable && typePolicy.enabled && (
              <p className='text-muted-foreground text-sm'>
                {text(
                  'Submitting will not charge you. Choose a payment method in the application details.'
                )}
              </p>
            )}
            {(!typePolicy.enabled || paymentBlocked) && (
              <p
                id='invoice-fee-warning'
                role='status'
                className='bg-muted rounded-lg p-3 text-sm'
              >
                {text(
                  !typePolicy.enabled
                    ? 'This invoice type is currently unavailable'
                    : 'This application requires a fee. It cannot be submitted until fee payment is available.'
                )}
              </p>
            )}
            {mutation.isError && (
              <p role='alert' className='text-destructive text-sm'>
                {getServerErrorMessage(mutation.error)}
              </p>
            )}
            <Button
              className='w-full'
              type='submit'
              disabled={disabled}
              aria-describedby={
                !typePolicy.enabled || paymentBlocked
                  ? 'invoice-fee-warning'
                  : undefined
              }
            >
              {text(submitLabel)}
            </Button>
          </fieldset>
        </form>
      </CardContent>
    </Card>
  )
}
