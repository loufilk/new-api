import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useFieldArray, useForm } from 'react-hook-form'
import { toast } from 'sonner'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'
import { useRequestId } from '@/features/agent-platform/hooks/use-request-id'
import { handleServerError } from '@/lib/server-error-message'

import { partnerKey, submitPartnerApplication } from './api'
import { QuoteFields } from './quote-fields'
import {
  partnerApplicationSchema,
  type PartnerApplicationFormValues,
} from './schema'
import type { PartnerApplicationInput } from './types'

const emptyQuote = {
  platform: 'openai' as const,
  group: '',
  rate: '1',
  models: '',
  description: '',
}

const defaults: PartnerApplicationFormValues = {
  site_name: '',
  site_url: '',
  contact_type: 'wechat',
  contact: '',
  description: '',
  quotes: [{ ...emptyQuote }],
}

export function PartnerApplicationForm(props: {
  owner: number
  disabled: boolean
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const identify = useRequestId()
  const form = useForm<PartnerApplicationFormValues>({
    resolver: zodResolver(partnerApplicationSchema),
    defaultValues: defaults,
  })
  const quotes = useFieldArray({ control: form.control, name: 'quotes' })
  const mutation = useMutation({
    gcTime: 0,
    mutationFn: (request: { input: PartnerApplicationInput; key: string }) =>
      submitPartnerApplication(request.input, request.key),
    onSuccess: () => {
      form.reset(defaults)
      identify.reset()
      void client.invalidateQueries({
        queryKey: [...partnerKey, props.owner],
      })
      toast.success(t('Partner application submitted.'))
    },
    onError: handleServerError,
    meta: { errorToast: false },
  })
  const disabled = props.disabled || mutation.isPending
  const errors = form.formState.errors

  return (
    <Card className='min-w-0'>
      <CardHeader>
        <CardTitle>{t('Submit a quote')}</CardTitle>
        <p className='text-muted-foreground text-sm'>
          {t('Share your site, contact details, groups and rates.')}
        </p>
      </CardHeader>
      <CardContent>
        <form
          noValidate
          onSubmit={form.handleSubmit((values) => {
            if (disabled) return
            const input: PartnerApplicationInput = {
              ...values,
              quotes: values.quotes.map((quote) => ({
                ...quote,
                rate: quote.rate,
              })),
            }
            mutation.mutate({ input, key: identify(input) })
          })}
        >
          <fieldset disabled={disabled} className='flex min-w-0 flex-col gap-4'>
            <FieldGroup className='grid min-w-0 gap-3 sm:grid-cols-2'>
              <Field data-invalid={Boolean(errors.site_name)}>
                <FieldLabel htmlFor='partner-site-name' required>
                  {t('Site name')}
                </FieldLabel>
                <Input
                  id='partner-site-name'
                  {...form.register('site_name')}
                  required
                  maxLength={120}
                  aria-invalid={Boolean(errors.site_name)}
                />
                <FieldError>
                  {errors.site_name?.message
                    ? t(errors.site_name.message)
                    : undefined}
                </FieldError>
              </Field>
              <Field data-invalid={Boolean(errors.site_url)}>
                <FieldLabel htmlFor='partner-site-url'>
                  {t('Site address')}
                </FieldLabel>
                <Input
                  id='partner-site-url'
                  {...form.register('site_url')}
                  type='url'
                  maxLength={2048}
                  placeholder='https://'
                  aria-invalid={Boolean(errors.site_url)}
                />
                <FieldError>
                  {errors.site_url?.message
                    ? t(errors.site_url.message)
                    : undefined}
                </FieldError>
              </Field>
              <Field>
                <FieldLabel htmlFor='partner-contact-type' required>
                  {t('Contact method')}
                </FieldLabel>
                <NativeSelect
                  id='partner-contact-type'
                  {...form.register('contact_type')}
                  required
                >
                  <NativeSelectOption value='wechat'>{t('WeChat')}</NativeSelectOption>
                  <NativeSelectOption value='qq'>{t('QQ')}</NativeSelectOption>
                  <NativeSelectOption value='email'>{t('Email')}</NativeSelectOption>
                  <NativeSelectOption value='phone'>{t('Phone')}</NativeSelectOption>
                  <NativeSelectOption value='other'>{t('Other')}</NativeSelectOption>
                </NativeSelect>
              </Field>
              <Field data-invalid={Boolean(errors.contact)}>
                <FieldLabel htmlFor='partner-contact' required>
                  {t('Contact details')}
                </FieldLabel>
                <Input
                  id='partner-contact'
                  {...form.register('contact')}
                  required
                  maxLength={200}
                  aria-invalid={Boolean(errors.contact)}
                />
                <FieldError>
                  {errors.contact?.message
                    ? t(errors.contact.message)
                    : undefined}
                </FieldError>
              </Field>
              <Field className='sm:col-span-2'>
                <FieldLabel htmlFor='partner-description'>
                  {t('Notes')}
                </FieldLabel>
                <Textarea
                  id='partner-description'
                  {...form.register('description')}
                  maxLength={3000}
                  rows={3}
                />
              </Field>
            </FieldGroup>
            <FieldSet className='min-w-0 gap-3'>
              <FieldLegend
                variant='label'
                className='flex w-full items-center justify-between gap-2'
              >
                {t('Available groups')}
                <span className='text-muted-foreground text-xs'>
                  {t('{{current}} / {{maximum}} maximum', {
                    current: quotes.fields.length,
                    maximum: 20,
                  })}
                </span>
              </FieldLegend>
              {quotes.fields.map((field, index) => {
                const quoteErrors = errors.quotes?.[index]
                return (
                  <QuoteFields
                    key={field.id}
                    index={index}
                    register={form.register}
                    errors={{
                      platform: quoteErrors?.platform?.message,
                      group: quoteErrors?.group?.message,
                      rate: quoteErrors?.rate?.message,
                      models: quoteErrors?.models?.message,
                      description: quoteErrors?.description?.message,
                    }}
                    disabled={disabled}
                    canRemove={quotes.fields.length > 1}
                    onRemove={() => quotes.remove(index)}
                  />
                )
              })}
              <FieldError>
                {errors.quotes?.root?.message
                  ? t(errors.quotes.root.message)
                  : undefined}
              </FieldError>
              <Button
                type='button'
                variant='outline'
                size='sm'
                disabled={disabled || quotes.fields.length >= 20}
                onClick={() => quotes.append({ ...emptyQuote })}
              >
                {t('Add group')}
              </Button>
            </FieldSet>
            <p className='text-muted-foreground text-xs'>
              {t('Do not include API keys. Submitted applications cannot be edited.')}
            </p>
            <div className='flex justify-end'>
              <Button type='submit' disabled={disabled}>
                {mutation.isPending
                  ? t('Submitting…')
                  : t('Submit partner application')}
              </Button>
            </div>
          </fieldset>
        </form>
      </CardContent>
    </Card>
  )
}
