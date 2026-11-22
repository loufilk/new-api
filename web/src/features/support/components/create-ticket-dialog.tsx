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
import { Loader2 } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'
import { useAuthStore } from '@/stores/auth-store'

import { useSupportImages } from '../hooks/use-support-images'
import { useSupportRequestId } from '../hooks/use-support-request-id'
import {
  SUPPORT_TICKET_CATEGORIES,
  supportTicketCategoryLabel,
} from '../lib/categories'
import {
  createSupportTicketSchema,
  type CreateSupportTicketValues,
} from '../lib/schema'
import type { SupportTicketCreateSubmission } from '../types'
import { SupportImagePicker } from './support-image-picker'

interface CreateTicketDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (values: SupportTicketCreateSubmission) => Promise<void>
  pending: boolean
}

export function CreateTicketDialog(props: CreateTicketDialogProps) {
  const { t } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const accountEmail = useAuthStore((state) => state.auth.user?.email ?? '')
  const images = useSupportImages()
  const { get: requestId, reset: resetRequestId } = useSupportRequestId()
  const submitting = useRef(false)
  const [localPending, setLocalPending] = useState(false)
  const form = useForm<CreateSupportTicketValues>({
    resolver: zodResolver(createSupportTicketSchema),
    defaultValues: {
      subject: '',
      message: '',
      category: 'api_model',
      contact_email: accountEmail,
    },
  })
  const accountEmailRef = useRef(accountEmail)
  accountEmailRef.current = accountEmail
  const { clear: clearImages } = images
  useEffect(() => {
    form.reset({
      subject: '',
      message: '',
      category: 'api_model',
      contact_email: accountEmailRef.current,
    })
    clearImages()
    resetRequestId()
  }, [form, props.open, userId, clearImages, resetRequestId])

  const busy = props.pending || localPending || form.formState.isSubmitting
  const submit = form.handleSubmit(async (values) => {
    if (submitting.current || props.pending) return
    submitting.current = true
    setLocalPending(true)
    const files = images.items.map((item) => item.file)
    try {
      await props.onSubmit({
        ...values,
        images: files,
        request_id: requestId(JSON.stringify(values), files),
      })
      clearImages()
      resetRequestId()
    } catch {
      // Keep the draft and request ID for an uncertain retry; the mutation shows errors.
    } finally {
      submitting.current = false
      setLocalPending(false)
    }
  })

  return (
    <Dialog
      open={props.open}
      onOpenChange={(open) => {
        if (!busy && !submitting.current) props.onOpenChange(open)
      }}
      showCloseButton={!busy}
      title={t('New ticket')}
      description={t(
        'Describe the issue clearly. Support replies will appear in this conversation.'
      )}
      footer={
        <>
          <Button
            variant='outline'
            type='button'
            onClick={() => props.onOpenChange(false)}
            disabled={busy}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='submit'
            form='create-support-ticket-form'
            disabled={busy}
          >
            {busy && <Loader2 className='animate-spin' aria-hidden='true' />}
            {t('Create ticket')}
          </Button>
        </>
      }
    >
      <form id='create-support-ticket-form' onSubmit={submit}>
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor='support-ticket-category'>
              {t('Problem type')}
            </FieldLabel>
            <NativeSelect
              id='support-ticket-category'
              className='w-full'
              disabled={busy}
              {...form.register('category')}
            >
              {SUPPORT_TICKET_CATEGORIES.map((category) => (
                <NativeSelectOption key={category} value={category}>
                  {supportTicketCategoryLabel(t, category)}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </Field>
          <Field>
            <FieldLabel htmlFor='support-ticket-subject'>
              {t('Subject')}
            </FieldLabel>
            <Input
              id='support-ticket-subject'
              maxLength={200}
              disabled={busy}
              aria-invalid={Boolean(form.formState.errors.subject)}
              {...form.register('subject')}
            />
            {form.formState.errors.subject && (
              <p className='text-destructive text-xs' role='alert'>
                {t('Subject is required and must not exceed 200 characters')}
              </p>
            )}
          </Field>
          <Field>
            <FieldLabel htmlFor='support-ticket-contact'>
              {t('Contact email (optional)')}
            </FieldLabel>
            <Input
              id='support-ticket-contact'
              type='email'
              autoComplete='email'
              maxLength={254}
              disabled={busy}
              aria-describedby='support-contact-help'
              aria-invalid={Boolean(form.formState.errors.contact_email)}
              {...form.register('contact_email')}
            />
            <FieldDescription id='support-contact-help'>
              {t(
                'For reference only. Automatic notifications use your verified account email, not this contact address. Leave blank to omit it.'
              )}
            </FieldDescription>
            {form.formState.errors.contact_email && (
              <p className='text-destructive text-xs' role='alert'>
                {t('Enter a valid contact email or leave it blank.')}
              </p>
            )}
          </Field>
          <Field>
            <FieldLabel htmlFor='support-ticket-message'>
              {t('Message')}
            </FieldLabel>
            <Textarea
              id='support-ticket-message'
              rows={5}
              maxLength={10000}
              disabled={busy}
              aria-invalid={Boolean(form.formState.errors.message)}
              {...form.register('message')}
            />
            {form.formState.errors.message && (
              <p className='text-destructive text-xs' role='alert'>
                {t('Message is required and must not exceed 10000 characters')}
              </p>
            )}
          </Field>
          <SupportImagePicker
            items={images.items}
            error={images.error}
            disabled={busy}
            onAdd={images.add}
            onRemove={images.remove}
          />
        </FieldGroup>
      </form>
    </Dialog>
  )
}
