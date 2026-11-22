import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { formatTimestamp } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'

import {
  getPartnerApplication,
  partnerKey,
  privateRead,
  reviewPartnerApplication,
} from './api'
import type { PartnerApplicationStatus } from './types'

function statusTitle(
  status: PartnerApplicationStatus,
  t: (key: string) => string
) {
  switch (status) {
    case 'submitted':
      return t('Submitted')
    case 'reviewing':
      return t('Reviewing')
    case 'approved':
      return t('Approved')
    case 'rejected':
      return t('Rejected')
    case 'cancelled':
      return t('Cancelled')
  }
}

function contactTitle(type: string, t: (key: string) => string) {
  switch (type) {
    case 'wechat':
      return t('WeChat')
    case 'qq':
      return t('QQ')
    case 'email':
      return t('Email')
    case 'phone':
      return t('Phone')
    case 'other':
      return t('Other')
    default:
      return t('Other')
  }
}

function platformTitle(platform: string, t: (key: string) => string) {
  switch (platform) {
    case 'openai':
      return t('OpenAI')
    case 'anthropic':
      return t('Anthropic')
    case 'google':
      return t('Google Gemini')
    case 'deepseek':
      return t('DeepSeek')
    case 'alibaba':
      return t('Alibaba Cloud')
    case 'bytedance':
      return t('ByteDance')
    case 'zhipu':
      return t('Zhipu')
    case 'moonshot':
      return t('Moonshot')
    case 'other':
      return t('Other')
    default:
      return t('Other')
  }
}

export function PartnerApplicationDetailDialog(props: {
  id: number | null
  onClose: () => void
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [note, setNote] = useState('')
  const query = useQuery({
    ...privateRead,
    queryKey: [...partnerKey, 'admin', 'detail', props.id],
    queryFn: ({ signal }) =>
      getPartnerApplication(true, props.id as number, signal),
    enabled: props.id !== null,
  })
  const review = useMutation({
    gcTime: 0,
    mutationFn: (action: 'review' | 'approve' | 'reject') => {
      if (!query.data) throw new Error('Partner application is unavailable.')
      return reviewPartnerApplication({
        id: query.data.id,
        expectedRevision: query.data.revision,
        action,
        note,
      })
    },
    onSuccess: (_application, action) => {
      void client.invalidateQueries({ queryKey: partnerKey })
      toast.success(
        action === 'review'
          ? t('Application moved to review.')
          : t('Partner application decision saved.')
      )
      props.onClose()
    },
    onError: handleServerError,
    meta: { errorToast: false },
  })
  const application = query.data

  return (
    <Dialog
      open={props.id !== null}
      onOpenChange={(open) => {
        if (!open && !review.isPending) props.onClose()
      }}
      title={
        application
          ? t('Partner application #{{id}}', { id: application.id })
          : t('Partner application')
      }
      description={application ? application.site_name : undefined}
      contentClassName='sm:max-w-3xl'
    >
      {query.isPending && (
        <p className='text-muted-foreground'>{t('Loading...')}</p>
      )}
      {query.isError && (
        <Alert variant='destructive'>
          <AlertDescription className='flex flex-wrap items-center gap-2'>
            {t('Unable to load partner application')}
            <Button
              size='sm'
              variant='outline'
              onClick={() => void query.refetch()}
            >
              {t('Retry')}
            </Button>
          </AlertDescription>
        </Alert>
      )}
      {application && (
        <div className='flex flex-col gap-5'>
          <div className='flex flex-wrap items-center justify-between gap-2'>
            <Badge
              variant={
                application.status === 'approved' ? 'default' : 'warning'
              }
            >
              {statusTitle(application.status, t)}
            </Badge>
            <span className='text-muted-foreground text-sm'>
              {t('User ID')}: {application.user_id} ·{' '}
              {formatTimestamp(application.created_at)}
            </span>
          </div>
          <dl className='grid gap-x-4 gap-y-3 sm:grid-cols-2'>
            <div className='min-w-0'>
              <dt className='text-muted-foreground text-xs'>
                {t('Site address')}
              </dt>
              <dd className='text-sm break-all'>
                {application.site_url ? (
                  <a
                    className='text-primary underline underline-offset-2'
                    href={application.site_url}
                    target='_blank'
                    rel='noreferrer noopener'
                  >
                    {application.site_url}
                  </a>
                ) : (
                  t('Not provided')
                )}
              </dd>
            </div>
            <div className='min-w-0'>
              <dt className='text-muted-foreground text-xs'>
                {t('Contact method')}
              </dt>
              <dd className='text-sm break-words'>
                {contactTitle(application.contact_type, t)}:{' '}
                {application.contact}
              </dd>
            </div>
            <div className='min-w-0 sm:col-span-2'>
              <dt className='text-muted-foreground text-xs'>{t('Notes')}</dt>
              <dd className='text-sm whitespace-pre-wrap'>
                {application.description || t('Not provided')}
              </dd>
            </div>
          </dl>
          <section className='flex flex-col gap-3'>
            <h3 className='text-sm font-medium'>{t('Submitted quotes')}</h3>
            <div className='divide-y border-y'>
              {application.quotes.map((quote) => (
                <article
                  key={`${application.id}-${quote.group}`}
                  className='grid min-w-0 gap-1 py-3 sm:grid-cols-[minmax(0,1fr)_auto]'
                >
                  <div className='min-w-0'>
                    <p className='text-sm font-medium break-words'>
                      {platformTitle(quote.platform, t)} / {quote.group}
                    </p>
                    {quote.models && (
                      <p className='text-muted-foreground text-xs break-words'>
                        {t('Main models')}: {quote.models}
                      </p>
                    )}
                    {quote.description && (
                      <p className='text-muted-foreground text-xs whitespace-pre-wrap'>
                        {quote.description}
                      </p>
                    )}
                  </div>
                  <span className='tabular-nums'>×{quote.rate}</span>
                </article>
              ))}
            </div>
          </section>
          {application.review_note && (
            <Alert>
              <AlertDescription>
                <span className='font-medium'>{t('Review note')}:</span>{' '}
                {application.review_note}
              </AlertDescription>
            </Alert>
          )}
          {application.events?.length ? (
            <section className='flex flex-col gap-2'>
              <h3 className='text-sm font-medium'>
                {t('Application history')}
              </h3>
              <ol className='flex flex-col gap-2'>
                {application.events.map((event) => (
                  <li key={event.id} className='border-s-2 ps-3 text-sm'>
                    <span>{statusTitle(event.to_status, t)}</span>
                    <span className='text-muted-foreground ms-2 text-xs'>
                      {formatTimestamp(event.created_at)} · {t('User ID')}{' '}
                      {event.actor_id}
                    </span>
                    {event.note && (
                      <p className='text-muted-foreground text-xs whitespace-pre-wrap'>
                        {event.note}
                      </p>
                    )}
                  </li>
                ))}
              </ol>
            </section>
          ) : null}
          {(application.status === 'submitted' ||
            application.status === 'reviewing') && (
            <section className='flex flex-col gap-3 border-t pt-4'>
              <div className='flex flex-col gap-1.5'>
                <Label htmlFor='partner-review-note'>{t('Review note')}</Label>
                <Textarea
                  id='partner-review-note'
                  value={note}
                  onChange={(event) => setNote(event.target.value)}
                  maxLength={1000}
                  rows={3}
                  disabled={review.isPending}
                />
              </div>
              <div className='flex flex-wrap justify-end gap-2'>
                {application.status === 'submitted' ? (
                  <Button
                    variant='outline'
                    disabled={review.isPending}
                    onClick={() => review.mutate('review')}
                  >
                    {t('Start review')}
                  </Button>
                ) : (
                  <>
                    <Button
                      variant='outline'
                      disabled={review.isPending || !note.trim()}
                      onClick={() => review.mutate('reject')}
                    >
                      {t('Reject application')}
                    </Button>
                    <Button
                      disabled={review.isPending || !note.trim()}
                      onClick={() => review.mutate('approve')}
                    >
                      {t('Approve application')}
                    </Button>
                  </>
                )}
              </div>
            </section>
          )}
        </div>
      )}
    </Dialog>
  )
}
