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
import { useEffect } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'

import {
  createSupportTicketSchema,
  type CreateSupportTicketValues,
} from '../lib/schema'

interface CreateTicketDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (values: CreateSupportTicketValues) => Promise<void>
  pending: boolean
}

export function CreateTicketDialog(props: CreateTicketDialogProps) {
  const { t } = useTranslation()
  const form = useForm<CreateSupportTicketValues>({
    resolver: zodResolver(createSupportTicketSchema),
    defaultValues: { subject: '', message: '' },
  })

  useEffect(() => {
    if (!props.open) form.reset()
  }, [form, props.open])

  const submit = form.handleSubmit(async (values) => {
    await props.onSubmit(values)
  })

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
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
            disabled={props.pending}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='submit'
            form='create-support-ticket-form'
            disabled={props.pending}
          >
            {props.pending && (
              <Loader2 className='animate-spin' aria-hidden='true' />
            )}
            {t('Create ticket')}
          </Button>
        </>
      }
    >
      <form
        id='create-support-ticket-form'
        onSubmit={submit}
        className='space-y-4'
      >
        <div className='space-y-1.5'>
          <Label htmlFor='support-ticket-subject'>{t('Subject')}</Label>
          <Input
            id='support-ticket-subject'
            maxLength={200}
            aria-invalid={Boolean(form.formState.errors.subject)}
            {...form.register('subject')}
          />
          {form.formState.errors.subject && (
            <p className='text-destructive text-xs' role='alert'>
              {t('Subject is required and must not exceed 200 characters')}
            </p>
          )}
        </div>
        <div className='space-y-1.5'>
          <Label htmlFor='support-ticket-message'>{t('Message')}</Label>
          <Textarea
            id='support-ticket-message'
            rows={7}
            maxLength={10000}
            aria-invalid={Boolean(form.formState.errors.message)}
            {...form.register('message')}
          />
          {form.formState.errors.message && (
            <p className='text-destructive text-xs' role='alert'>
              {t('Message is required and must not exceed 10000 characters')}
            </p>
          )}
        </div>
      </form>
    </Dialog>
  )
}
