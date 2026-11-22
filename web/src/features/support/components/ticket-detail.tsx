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
import { CheckCircle2, Loader2, Send } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Field, FieldLabel } from '@/components/ui/field'
import { Textarea } from '@/components/ui/textarea'
import dayjs from '@/lib/dayjs'
import { cn } from '@/lib/utils'

import { useSupportImages } from '../hooks/use-support-images'
import { useSupportRequestId } from '../hooks/use-support-request-id'
import { supportTicketCategoryLabel } from '../lib/categories'
import {
  replySupportTicketSchema,
  type ReplySupportTicketValues,
} from '../lib/schema'
import {
  supportTicketStatusLabel,
  supportTicketStatusVariant,
} from '../lib/status'
import type {
  SupportTicketDetail,
  SupportTicketStatus,
  SupportTicketSubmission,
} from '../types'
import { SupportAttachments } from './support-attachments'
import { SupportImagePicker } from './support-image-picker'

interface TicketDetailProps {
  admin: boolean
  detail: SupportTicketDetail
  replyPending: boolean
  statusPending: boolean
  onReply: (submission: SupportTicketSubmission) => Promise<void>
  onStatusChange: (status: SupportTicketStatus) => Promise<void>
}

export function TicketDetail(props: TicketDetailProps) {
  const { t } = useTranslation()
  const images = useSupportImages()
  const { clear: clearImages } = images
  const { get: requestId, reset: resetRequestId } = useSupportRequestId()
  const submitting = useRef(false)
  const [localPending, setLocalPending] = useState(false)
  const form = useForm<ReplySupportTicketValues>({
    resolver: zodResolver(replySupportTicketSchema),
    defaultValues: { message: '' },
  })

  useEffect(() => {
    form.reset()
    clearImages()
    resetRequestId()
  }, [form, props.detail.ticket.id, clearImages, resetRequestId])

  const submitReply = form.handleSubmit(async (values) => {
    if (submitting.current || props.replyPending || props.statusPending) return
    submitting.current = true
    setLocalPending(true)
    const files = images.items.map((item) => item.file)
    try {
      await props.onReply({
        message: values.message,
        images: files,
        request_id: requestId(
          JSON.stringify({ ticket_id: props.detail.ticket.id, ...values }),
          files
        ),
      })
      form.reset()
      clearImages()
      resetRequestId()
    } catch {
      // The mutation owns user-visible error handling. Keep the draft for retry.
    } finally {
      submitting.current = false
      setLocalPending(false)
    }
  })
  const busy =
    props.replyPending ||
    props.statusPending ||
    localPending ||
    form.formState.isSubmitting
  const resolved = props.detail.ticket.status === 'resolved'
  const customerLabel = props.admin ? t('Customer') : t('You')

  return (
    <Card className='min-h-0'>
      <CardHeader className='border-b'>
        <div className='flex flex-wrap items-start justify-between gap-3'>
          <div className='min-w-0'>
            <CardTitle className='break-words'>
              {props.detail.ticket.subject}
            </CardTitle>
            <CardDescription className='mt-1'>
              {t('Ticket #{{id}}', { id: props.detail.ticket.id })}
              {props.admin
                ? ` · ${t('User #{{id}}', { id: props.detail.ticket.user_id })}`
                : ''}
              {' · '}
              {dayjs
                .unix(props.detail.ticket.created_at)
                .format('YYYY-MM-DD HH:mm')}
            </CardDescription>
          </div>
          <Badge
            variant={supportTicketStatusVariant(props.detail.ticket.status)}
          >
            {supportTicketStatusLabel(t, props.detail.ticket.status)}
          </Badge>
        </div>
        <div className='text-muted-foreground flex flex-wrap gap-x-4 gap-y-1 text-xs'>
          <span>
            {t('Problem type')}:{' '}
            {supportTicketCategoryLabel(t, props.detail.ticket.category)}
          </span>
          {props.detail.ticket.contact_email && (
            <span className='break-all'>
              {t('Contact email (reference only)')}:{' '}
              {props.detail.ticket.contact_email}
            </span>
          )}
        </div>
      </CardHeader>
      <CardContent className='flex min-h-0 flex-col gap-4'>
        <div className='max-h-[45vh] space-y-3 overflow-y-auto pr-1'>
          {props.detail.messages.map((message) => {
            const fromAdmin = message.author_role === 'admin'
            return (
              <div
                key={message.id}
                className={cn(
                  'flex',
                  fromAdmin ? 'justify-start' : 'justify-end'
                )}
              >
                <div
                  className={cn(
                    'max-w-[88%] rounded-xl border px-3 py-2.5',
                    fromAdmin ? 'bg-muted/50' : 'bg-primary/5'
                  )}
                >
                  <div className='mb-1 flex items-center gap-2 text-xs'>
                    <span className='font-medium'>
                      {fromAdmin ? t('Support') : customerLabel}
                    </span>
                    <span className='text-muted-foreground'>
                      {dayjs
                        .unix(message.created_at)
                        .format('YYYY-MM-DD HH:mm')}
                    </span>
                  </div>
                  <p className='text-sm break-words whitespace-pre-wrap'>
                    {message.content}
                  </p>
                  <SupportAttachments
                    admin={props.admin}
                    ticketId={props.detail.ticket.id}
                    attachments={message.attachments}
                  />
                </div>
              </div>
            )
          })}
        </div>

        {resolved && !props.admin ? (
          <div className='bg-muted/40 rounded-lg border p-3 text-sm'>
            <div className='flex items-center gap-2 font-medium'>
              <CheckCircle2 className='size-4' aria-hidden='true' />
              {t('This ticket has been resolved')}
            </div>
            <p className='text-muted-foreground mt-1'>
              {t('If more help is needed, create a new ticket.')}
            </p>
          </div>
        ) : (
          <form onSubmit={submitReply} className='space-y-2'>
            <Field>
              <FieldLabel htmlFor='support-ticket-reply'>
                {t('Reply')}
              </FieldLabel>
              <Textarea
                id='support-ticket-reply'
                disabled={busy}
                rows={4}
                maxLength={10000}
                placeholder={t('Write a reply...')}
                aria-invalid={Boolean(form.formState.errors.message)}
                {...form.register('message')}
              />
              {form.formState.errors.message && (
                <p className='text-destructive text-xs' role='alert'>
                  {t('Reply is required and must not exceed 10000 characters')}
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
            <div className='flex flex-wrap justify-end gap-2'>
              {props.admin && (
                <Button
                  type='button'
                  variant='outline'
                  disabled={busy}
                  onClick={() =>
                    void props
                      .onStatusChange(resolved ? 'processing' : 'resolved')
                      .catch(() => {
                        /* Mutation owns the error notification. */
                      })
                  }
                >
                  {props.statusPending && (
                    <Loader2 className='animate-spin' aria-hidden='true' />
                  )}
                  {resolved ? t('Reopen ticket') : t('Resolve ticket')}
                </Button>
              )}
              <Button type='submit' disabled={busy}>
                {props.replyPending ? (
                  <Loader2 className='animate-spin' aria-hidden='true' />
                ) : (
                  <Send aria-hidden='true' />
                )}
                {t('Send reply')}
              </Button>
            </div>
          </form>
        )}
      </CardContent>
    </Card>
  )
}
