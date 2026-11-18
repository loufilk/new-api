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
import { useRef } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Field, FieldError, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { useAuthStore } from '@/stores/auth-store'

import { invoiceSchema } from '../lib/schema'
import type { CommerceConfig, InvoiceInput, InvoiceOrder } from '../types'
import { InvoiceMoney } from './application-status'

export function InvoiceCreateDialog(props: {
  config: CommerceConfig
  orders: InvoiceOrder[]
  pending: boolean
  onClose: () => void
  onSubmit: (input: InvoiceInput, requestKey: string) => Promise<void>
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const email = useAuthStore((state) => state.auth.user?.email ?? '')
  const request = useRef({ signature: '', key: '' })
  const form = useForm<InvoiceInput>({
    resolver: zodResolver(invoiceSchema),
    defaultValues: {
      order_ids: [],
      kind: 'general',
      title_type: 'company',
      title: '',
      tax_id: '',
      email,
      address: '',
      phone: '',
      bank_name: '',
      bank_account: '',
    },
  })
  const selected = form.watch('order_ids')
  const kind = form.watch('kind')
  const company = form.watch('title_type') === 'company'
  const selectedOrders = props.orders.filter((order) =>
    selected.includes(order.topup_id)
  )
  const exponent = selectedOrders[0]?.currency_exponent ?? 2
  const amount = selectedOrders.reduce(
    (sum, order) => sum + BigInt(order.amount_minor),
    0n
  )
  const feeBPS =
    kind === 'special'
      ? props.config.special_fee_bps
      : props.config.general_fee_bps
  const fee = (amount * BigInt(feeBPS) + 5000n) / 10000n
  const busy = props.pending || form.formState.isSubmitting
  const submit = form.handleSubmit(async (values) => {
    if (busy) return
    if (selectedOrders.some((order) => order.currency_exponent !== exponent)) {
      form.setError('order_ids', {
        message: 'Select orders with the same currency precision',
      })
      return
    }
    const signature = JSON.stringify(values)
    if (request.current.signature !== signature) {
      request.current = { signature, key: crypto.randomUUID() }
    }
    try {
      await props.onSubmit(values, request.current.key)
    } catch {
      /* Keep fields and key for a safe retry. */
    }
  })

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) props.onClose()
      }}
      title={t('Apply for an invoice')}
      description={t(
        'Select paid orders and confirm the invoice title. Submitted details are retained for review.'
      )}
      contentClassName='sm:max-w-4xl'
      showCloseButton={!busy}
      footer={
        <>
          <Button variant='outline' disabled={busy} onClick={props.onClose}>
            {t('Cancel')}
          </Button>
          <Button
            type='submit'
            form='invoice-application-form'
            disabled={busy || !props.orders.length}
          >
            {busy ? t('Submitting...') : t('Submit application')}
          </Button>
        </>
      }
    >
      <form
        id='invoice-application-form'
        onSubmit={submit}
        className='grid gap-6 lg:grid-cols-[1.1fr_1fr]'
      >
        <section
          className='min-w-0 space-y-3'
          aria-label={t('Eligible orders')}
        >
          <div className='flex items-center justify-between gap-2'>
            <h3 className='font-semibold'>{t('Eligible orders')}</h3>
            <span className='text-muted-foreground text-xs'>
              {t('{{count}} selected', { count: selected.length })}
            </span>
          </div>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Settled {{currency}} payments from the last {{days}} days. Bonus credits and previously reserved orders are excluded.',
              {
                currency: props.config.invoice_currency,
                days: props.config.invoice_window_days,
              }
            )}
          </p>
          {props.orders.length === 0 ? (
            <EmptyState
              title={t('No eligible orders')}
              description={t(
                'Paid orders with verified payment amounts will appear here.'
              )}
            />
          ) : (
            <div className='max-h-80 space-y-2 overflow-y-auto rounded-xl border p-2'>
              {props.orders.map((order) => (
                <label
                  key={order.topup_id}
                  className='hover:bg-muted flex cursor-pointer items-start gap-3 rounded-lg p-3'
                >
                  <Checkbox
                    aria-label={t('Select order {{number}}', {
                      number: order.trade_no,
                    })}
                    checked={selected.includes(order.topup_id)}
                    disabled={
                      busy ||
                      (!selected.includes(order.topup_id) &&
                        selected.length >= props.config.invoice_max_orders)
                    }
                    onCheckedChange={(checked) =>
                      form.setValue(
                        'order_ids',
                        checked
                          ? [...selected, order.topup_id]
                          : selected.filter((id) => id !== order.topup_id),
                        { shouldValidate: true }
                      )
                    }
                  />
                  <div className='min-w-0 flex-1'>
                    <p className='text-xs break-all'>{order.trade_no}</p>
                    <p className='mt-1 text-sm font-medium'>
                      <InvoiceMoney
                        minor={order.amount_minor}
                        currency={order.currency}
                        exponent={order.currency_exponent}
                      />
                    </p>
                  </div>
                </label>
              ))}
            </div>
          )}
          {form.formState.errors.order_ids?.message && (
            <p role='alert' className='text-destructive text-sm'>
              {t(form.formState.errors.order_ids.message)}
            </p>
          )}
          <dl className='bg-muted/40 grid gap-3 rounded-xl border p-4 text-sm'>
            <div className='flex justify-between gap-3'>
              <dt>{t('Invoice amount')}</dt>
              <dd>
                <InvoiceMoney
                  minor={amount.toString()}
                  currency={props.config.invoice_currency}
                  exponent={exponent}
                />
              </dd>
            </div>
            <div className='flex justify-between gap-3'>
              <dt>
                {t('Service fee')} ({formatNumber(feeBPS / 100, locale)}%)
              </dt>
              <dd>
                <InvoiceMoney
                  minor={fee.toString()}
                  currency={props.config.invoice_currency}
                  exponent={exponent}
                />
              </dd>
            </div>
          </dl>
          {fee > 0n && (
            <p className='text-muted-foreground text-xs'>
              {t(
                'The service fee is confirmed separately by the administrator. Your wallet will not be charged by this application.'
              )}
            </p>
          )}
        </section>
        <div className='grid content-start gap-4'>
          <div className='grid gap-3 sm:grid-cols-2'>
            <Field>
              <FieldLabel htmlFor='invoice-kind'>
                {t('Invoice type')}
              </FieldLabel>
              <NativeSelect
                id='invoice-kind'
                className='w-full'
                {...form.register('kind')}
                disabled={busy}
              >
                <NativeSelectOption value='general'>
                  {t('General invoice')}
                </NativeSelectOption>
                {props.config.special_invoice_enabled && (
                  <NativeSelectOption value='special'>
                    {t('Special invoice')}
                  </NativeSelectOption>
                )}
              </NativeSelect>
            </Field>
            <Field>
              <FieldLabel htmlFor='invoice-title-type'>
                {t('Title type')}
              </FieldLabel>
              <NativeSelect
                id='invoice-title-type'
                className='w-full'
                {...form.register('title_type')}
                disabled={busy}
              >
                <NativeSelectOption value='company'>
                  {t('Company')}
                </NativeSelectOption>
                <NativeSelectOption value='individual'>
                  {t('Individual')}
                </NativeSelectOption>
              </NativeSelect>
              <FieldError>
                {form.formState.errors.title_type?.message &&
                  t(form.formState.errors.title_type.message)}
              </FieldError>
            </Field>
          </div>
          {(
            [
              { name: 'title', label: 'Invoice title' },
              { name: 'email', label: 'Delivery email' },
              ...(company
                ? [{ name: 'tax_id', label: 'Tax identification number' }]
                : []),
              ...(kind === 'special'
                ? [
                    { name: 'address', label: 'Registered address' },
                    { name: 'phone', label: 'Registered phone' },
                    { name: 'bank_name', label: 'Bank name' },
                    { name: 'bank_account', label: 'Bank account' },
                  ]
                : []),
            ] as {
              name: Exclude<
                keyof InvoiceInput,
                'order_ids' | 'kind' | 'title_type'
              >
              label: string
            }[]
          ).map((field) => (
            <Field
              key={field.name}
              data-invalid={!!form.formState.errors[field.name]}
            >
              <FieldLabel htmlFor={`invoice-${field.name}`}>
                {t(field.label)}
              </FieldLabel>
              <Input
                id={`invoice-${field.name}`}
                {...form.register(field.name)}
                type={field.name === 'email' ? 'email' : 'text'}
                disabled={busy}
                aria-invalid={!!form.formState.errors[field.name]}
              />
              <FieldError>
                {form.formState.errors[field.name]?.message &&
                  t(form.formState.errors[field.name]?.message ?? '')}
              </FieldError>
            </Field>
          ))}
        </div>
      </form>
    </Dialog>
  )
}
