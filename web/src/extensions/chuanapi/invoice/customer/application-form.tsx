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

import { invoiceAPI } from './api'
import { useInvoiceText, type InvoiceTextKey } from './copy'
import {
  invoiceSchema,
  type InvoiceFormInput,
  type InvoiceFields,
} from './form-schema'
import type { OrderSelection } from './orders'

export function ApplicationForm(props: {
  selection: OrderSelection
  onBusy: (busy: boolean) => void
  onSuccess: () => void
  onConflict: () => void
}) {
  const { text, money } = useInvoiceText()
  const form = useForm<InvoiceFormInput, unknown, InvoiceFields>({
    resolver: zodResolver(invoiceSchema),
    defaultValues: {
      invoice_type: 'ordinary',
      title_type: 'company',
      category: 'technical_service',
      title: '',
      email: '',
    },
  })
  const [type, titleType] = useWatch({
    control: form.control,
    name: ['invoice_type', 'title_type'],
  })
  const identify = useRequestId()
  const mutation = useMutation({
    gcTime: 0,
    mutationFn: (fields: InvoiceFields) => {
      const ids = [...props.selection.keys()].sort((a, b) => a - b)
      return invoiceAPI.submit(fields, ids, identify([fields, ids]))
    },
    onSuccess: () => {
      form.reset()
      identify.reset()
      props.onSuccess()
      toast.success(text('Application submitted.'))
    },
    meta: { errorToast: false },
    onError: (error) => {
      if (getServerErrorStatus(error) === 409) props.onConflict()
    },
    onSettled: () => props.onBusy(false),
  })
  const special = type === 'special'
  const company = titleType === 'company'
  const total = [...props.selection.values()]
    .reduce((sum, amount) => sum + BigInt(amount), 0n)
    .toString()
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
            if (props.selection.size === 0 || mutation.isPending) return
            props.onBusy(true)
            mutation.mutate(values)
          })}
        >
          <fieldset className='space-y-4' disabled={mutation.isPending}>
            <div className='grid grid-cols-2 gap-3'>
              <div className='space-y-1.5'>
                <Label htmlFor='invoice-type'>{text('Invoice type')}</Label>
                <NativeSelect
                  className='w-full'
                  id='invoice-type'
                  {...form.register('invoice_type', {
                    onChange: (e) => {
                      if (e.target.value === 'special') {
                        form.setValue('title_type', 'company')
                      }
                    },
                  })}
                >
                  <NativeSelectOption value='ordinary'>
                    {text('Ordinary invoice')}
                  </NativeSelectOption>
                  <NativeSelectOption value='special'>
                    {text('Special VAT invoice')}
                  </NativeSelectOption>
                </NativeSelect>
              </div>
              <div className='space-y-1.5'>
                <Label htmlFor='invoice-title-type'>{text('Title type')}</Label>
                <NativeSelect
                  id='invoice-title-type'
                  className='w-full'
                  {...form.register('title_type')}
                >
                  <NativeSelectOption value='company'>
                    {text('Company')}
                  </NativeSelectOption>
                  <NativeSelectOption disabled={special} value='personal'>
                    {text('Individual')}
                  </NativeSelectOption>
                </NativeSelect>
              </div>
            </div>
            {field('title', 'Invoice title')}
            {company && field('tax_id', 'Tax ID')}
            <div className='space-y-1.5'>
              <Label htmlFor='invoice-category'>
                {text('Invoice category')}
              </Label>
              <NativeSelect
                id='invoice-category'
                className='w-full'
                {...form.register('category')}
              >
                <NativeSelectOption value='technical_service'>
                  {text('Technical services')}
                </NativeSelectOption>
                <NativeSelectOption value='software_development'>
                  {text('Software development')}
                </NativeSelectOption>
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
                <dd className='font-semibold tabular-nums'>{money(total)}</dd>
              </div>
            </dl>
            {mutation.isError && (
              <p role='alert' className='text-destructive text-sm'>
                {getServerErrorMessage(mutation.error)}
              </p>
            )}
            <Button
              className='w-full'
              type='submit'
              disabled={!props.selection.size || mutation.isPending}
            >
              {mutation.isPending
                ? text('Submitting…')
                : text('Submit application')}
            </Button>
          </fieldset>
        </form>
      </CardContent>
    </Card>
  )
}
