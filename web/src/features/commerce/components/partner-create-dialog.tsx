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
import { Plus, Trash2 } from 'lucide-react'
import { useRef } from 'react'
import { useFieldArray, useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Field, FieldError, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { useAuthStore } from '@/stores/auth-store'

import { partnerSchema } from '../lib/schema'
import type { PartnerInput } from '../types'

export function PartnerCreateDialog(props: {
  pending: boolean
  onClose: () => void
  onSubmit: (input: PartnerInput, requestKey: string) => Promise<void>
}) {
  const { t } = useTranslation()
  const email = useAuthStore((state) => state.auth.user?.email ?? '')
  const request = useRef({ signature: '', key: '' })
  const form = useForm<PartnerInput>({
    resolver: zodResolver(partnerSchema),
    defaultValues: {
      site_name: '',
      site_url: '',
      contact_name: '',
      contact_email: email,
      contact_method: '',
      description: '',
      quotes: [{ group: '', models: '', rate: '', description: '' }],
    },
  })
  const quotes = useFieldArray({ control: form.control, name: 'quotes' })
  const busy = props.pending || form.formState.isSubmitting
  const submit = form.handleSubmit(async (values) => {
    if (busy) return
    const signature = JSON.stringify(values)
    if (signature !== request.current.signature) {
      request.current = { signature, key: crypto.randomUUID() }
    }
    try {
      await props.onSubmit(values, request.current.key)
    } catch {
      /* Retain this submission for an idempotent retry. */
    }
  })

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) props.onClose()
      }}
      title={t('Apply as a supply partner')}
      description={t(
        'Introduce your service and submit group quotes. API credentials are not required.'
      )}
      contentClassName='sm:max-w-5xl'
      showCloseButton={!busy}
      footer={
        <>
          <Button variant='outline' disabled={busy} onClick={props.onClose}>
            {t('Cancel')}
          </Button>
          <Button form='partner-application-form' type='submit' disabled={busy}>
            {busy ? t('Submitting...') : t('Submit application')}
          </Button>
        </>
      }
    >
      <form
        id='partner-application-form'
        onSubmit={submit}
        className='grid gap-6 lg:grid-cols-[0.8fr_1.2fr]'
      >
        <div className='min-w-0 space-y-4'>
          {(
            [
              { name: 'site_name', label: 'Site name' },
              { name: 'site_url', label: 'Site URL' },
              { name: 'contact_name', label: 'Contact name' },
              { name: 'contact_email', label: 'Contact email' },
              { name: 'contact_method', label: 'Other contact method' },
            ] as const
          ).map((field) => (
            <Field key={field.name}>
              <FieldLabel htmlFor={`partner-${field.name}`}>
                {t(field.label)}
              </FieldLabel>
              <Input
                id={`partner-${field.name}`}
                {...form.register(field.name)}
                disabled={busy}
                aria-invalid={!!form.formState.errors[field.name]}
              />
              <FieldError>
                {form.formState.errors[field.name]?.message &&
                  t(form.formState.errors[field.name]?.message ?? '')}
              </FieldError>
            </Field>
          ))}
          <Field>
            <FieldLabel htmlFor='partner-description'>
              {t('Supply and service description')}
            </FieldLabel>
            <Textarea
              id='partner-description'
              rows={4}
              maxLength={3000}
              {...form.register('description')}
              disabled={busy}
              aria-invalid={!!form.formState.errors.description}
            />
            <FieldError>
              {form.formState.errors.description?.message &&
                t(form.formState.errors.description.message)}
            </FieldError>
          </Field>
        </div>
        <section className='min-w-0 space-y-3' aria-label={t('Group quotes')}>
          <div className='flex flex-wrap items-center justify-between gap-3'>
            <h3 className='font-semibold'>
              {t('Group quotes')} ({quotes.fields.length}/20)
            </h3>
            <Button
              type='button'
              variant='outline'
              size='sm'
              disabled={busy || quotes.fields.length >= 20}
              onClick={() =>
                quotes.append({
                  group: '',
                  models: '',
                  rate: '',
                  description: '',
                })
              }
            >
              <Plus aria-hidden />
              {t('Add quote')}
            </Button>
          </div>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Quote an absolute rate against the model base price. Describe protocol support and service limits.'
            )}
          </p>
          <div className='max-h-[65vh] space-y-3 overflow-y-auto pr-1'>
            {quotes.fields.map((item, index) => (
              <fieldset
                key={item.id}
                className='bg-muted/20 grid gap-3 rounded-xl border p-4'
              >
                <legend className='px-1 text-xs font-medium'>
                  {t('Quote {{number}}', { number: index + 1 })}
                </legend>
                <div className='flex justify-end'>
                  <Button
                    type='button'
                    variant='ghost'
                    size='icon'
                    aria-label={t('Remove quote {{number}}', {
                      number: index + 1,
                    })}
                    disabled={busy || quotes.fields.length <= 1}
                    onClick={() => quotes.remove(index)}
                  >
                    <Trash2 className='size-4' aria-hidden />
                  </Button>
                </div>
                {(
                  [
                    { name: 'group', label: 'Group name' },
                    { name: 'models', label: 'Supported models' },
                    { name: 'rate', label: 'Quoted multiplier' },
                    { name: 'description', label: 'Service notes' },
                  ] as const
                ).map((field) => (
                  <Field key={field.name}>
                    <FieldLabel htmlFor={`quote-${item.id}-${field.name}`}>
                      {t(field.label)}
                    </FieldLabel>
                    <Input
                      id={`quote-${item.id}-${field.name}`}
                      {...form.register(`quotes.${index}.${field.name}`)}
                      inputMode={field.name === 'rate' ? 'decimal' : 'text'}
                      disabled={busy}
                      aria-invalid={
                        !!form.formState.errors.quotes?.[index]?.[field.name]
                      }
                    />
                    <FieldError>
                      {form.formState.errors.quotes?.[index]?.[field.name]
                        ?.message &&
                        t(
                          form.formState.errors.quotes[index]?.[field.name]
                            ?.message ?? ''
                        )}
                    </FieldError>
                  </Field>
                ))}
              </fieldset>
            ))}
          </div>
        </section>
      </form>
    </Dialog>
  )
}
