import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { X } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  sideDrawerContentClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Textarea } from '@/components/ui/textarea'
import { formatTimestamp } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { getServerErrorStatus } from '@/lib/server-error-message'

import {
  getPartnerApplication,
  partnerKey,
  privateRead,
  reviewPartnerApplication,
} from './api'
import { contactTitle, platformTitle } from './catalog'
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

export function PartnerApplicationDetailDialog(props: {
  id: number | null
  reviewer: number
  canReview: boolean
  onClose: () => void
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [note, setNote] = useState('')
  const [conflict, setConflict] = useState(false)
  const query = useQuery({
    ...privateRead,
    queryKey: [...partnerKey, 'admin', props.reviewer, 'detail', props.id],
    queryFn: ({ signal }) =>
      getPartnerApplication(true, props.id as number, signal),
    enabled: props.id !== null,
  })
  const review = useMutation({
    gcTime: 0,
    retry: false,
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
    onError: (error) => {
      if (getServerErrorStatus(error) === 409) {
        setConflict(true)
        return
      }
      handleServerError(error)
    },
    meta: { errorToast: false },
  })
  const application = query.isError ? undefined : query.data
  const canAct =
    application?.status === 'submitted' || application?.status === 'reviewing'
  const actionDisabled = query.isFetching || review.isPending || conflict

  return (
    <Sheet
      open={props.id !== null}
      onOpenChange={(open) => {
        if (!open && !review.isPending) props.onClose()
      }}
    >
      <SheetContent
        side='right'
        className={sideDrawerContentClassName('sm:max-w-2xl')}
        showCloseButton={false}
      >
        <SheetHeader
          className={sideDrawerHeaderClassName('relative pe-12 sm:pe-12')}
        >
          <SheetTitle>
            {application
              ? t('Partner application #{{id}}', { id: application.id })
              : t('Partner application')}
          </SheetTitle>
          {application && (
            <SheetDescription className='[overflow-wrap:anywhere]'>
              {application.site_name}
            </SheetDescription>
          )}
          <SheetClose
            render={
              <Button
                type='button'
                size='icon-sm'
                variant='ghost'
                className='absolute end-3 top-3'
                aria-label={t('Close')}
                disabled={review.isPending}
              />
            }
          >
            <X aria-hidden='true' />
          </SheetClose>
        </SheetHeader>
        <div className={sideDrawerFormClassName('gap-4')}>
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
              <section className='flex min-w-0 flex-col gap-3'>
                <h3 className='text-sm font-semibold'>
                  {t('Site information')}
                </h3>
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
                    <dd className='text-sm [overflow-wrap:anywhere]'>
                      {contactTitle(application.contact_type, t)}:{' '}
                      {application.contact}
                    </dd>
                  </div>
                  <div className='min-w-0 sm:col-span-2'>
                    <dt className='text-muted-foreground text-xs'>
                      {t('Notes')}
                    </dt>
                    <dd className='text-sm [overflow-wrap:anywhere] whitespace-pre-wrap'>
                      {application.description || t('Not provided')}
                    </dd>
                  </div>
                </dl>
              </section>
              <section className='flex flex-col gap-3'>
                <h3 className='text-sm font-medium'>{t('Submitted quotes')}</h3>
                <div className='divide-y border-y'>
                  {application.quotes.map((quote) => (
                    <article
                      key={`${application.id}-${quote.group}`}
                      className='grid min-w-0 gap-1 py-3 sm:grid-cols-[minmax(0,1fr)_auto]'
                    >
                      <div className='min-w-0'>
                        <p className='text-sm font-medium [overflow-wrap:anywhere]'>
                          {platformTitle(quote.platform, t)} / {quote.group}
                        </p>
                        {quote.models && (
                          <p className='text-muted-foreground text-xs [overflow-wrap:anywhere]'>
                            {t('Main models')}: {quote.models}
                          </p>
                        )}
                        {quote.description && (
                          <p className='text-muted-foreground text-xs [overflow-wrap:anywhere] whitespace-pre-wrap'>
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
                          <p className='text-muted-foreground text-xs [overflow-wrap:anywhere] whitespace-pre-wrap'>
                            {event.note}
                          </p>
                        )}
                      </li>
                    ))}
                  </ol>
                </section>
              ) : null}
              {canAct && !props.canReview && (
                <p className='text-muted-foreground text-sm'>
                  {t('This application is read-only for your account.')}
                </p>
              )}
              {props.canReview && canAct && (
                <section className='flex flex-col gap-3 border-t pt-4'>
                  {conflict && (
                    <Alert>
                      <AlertDescription className='flex flex-wrap items-center gap-2'>
                        {t(
                          'This application changed while you were reviewing it. Load the latest status before continuing.'
                        )}
                        <Button
                          size='sm'
                          variant='outline'
                          disabled={query.isFetching}
                          onClick={async () => {
                            const latest = await query.refetch()
                            if (latest.isSuccess) setConflict(false)
                          }}
                        >
                          {t('Reload latest application')}
                        </Button>
                      </AlertDescription>
                    </Alert>
                  )}
                  <div className='flex flex-col gap-1.5'>
                    <Label htmlFor='partner-review-note'>
                      {t('Review note')}
                    </Label>
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
                        disabled={actionDisabled}
                        onClick={() => review.mutate('review')}
                      >
                        {t('Start review')}
                      </Button>
                    ) : (
                      <>
                        <Button
                          variant='outline'
                          disabled={actionDisabled || !note.trim()}
                          onClick={() => review.mutate('reject')}
                        >
                          {t('Reject application')}
                        </Button>
                        <Button
                          disabled={actionDisabled || !note.trim()}
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
        </div>
      </SheetContent>
    </Sheet>
  )
}
