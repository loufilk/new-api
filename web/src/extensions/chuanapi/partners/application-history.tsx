import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { SimpleRecordPager } from '@/features/agent-platform/components/simple-record-pager'
import { formatTimestamp } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'

import {
  cancelPartnerApplication,
  listPartnerApplications,
  PARTNER_PAGE_SIZE,
  partnerKey,
  privateRead,
} from './api'
import { contactTitle, platformTitle } from './catalog'
import type { PartnerApplication, PartnerApplicationStatus } from './types'

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

function statusVariant(status: PartnerApplicationStatus) {
  if (status === 'approved') return 'default' as const
  if (status === 'rejected' || status === 'cancelled') {
    return 'secondary' as const
  }
  return 'warning' as const
}

function ApplicationRow(props: {
  application: PartnerApplication
  onCancel: (application: PartnerApplication) => void
}) {
  const { t } = useTranslation()
  const application = props.application
  const canCancel =
    application.status === 'submitted' || application.status === 'reviewing'
  return (
    <article className='flex flex-col gap-3 border-b py-4 last:border-b-0'>
      <div className='flex flex-wrap items-start justify-between gap-3'>
        <div className='flex min-w-0 flex-col gap-1'>
          <h3 className='font-medium break-words'>{application.site_name}</h3>
          <p className='text-muted-foreground text-xs'>
            {t('Submitted {{time}}', {
              time: formatTimestamp(application.created_at),
            })}
          </p>
        </div>
        <div className='flex shrink-0 items-center gap-2'>
          <Badge variant={statusVariant(application.status)}>
            {statusTitle(application.status, t)}
          </Badge>
          {canCancel && (
            <Button
              size='sm'
              variant='outline'
              onClick={() => props.onCancel(application)}
            >
              {t('Cancel partner application')}
            </Button>
          )}
        </div>
      </div>
      <dl className='grid gap-x-4 gap-y-2 text-sm sm:grid-cols-2'>
        <div className='min-w-0'>
          <dt className='text-muted-foreground'>{t('Site address')}</dt>
          <dd className='break-all'>
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
          <dt className='text-muted-foreground'>{t('Contact')}</dt>
          <dd className='break-words'>
            {contactTitle(application.contact_type, t)}: {application.contact}
          </dd>
        </div>
      </dl>
      {application.description && (
        <p className='text-muted-foreground text-sm whitespace-pre-wrap'>
          {application.description}
        </p>
      )}
      <ul className='flex flex-col gap-2'>
        {application.quotes.map((quote) => (
          <li
            key={`${application.id}-${quote.group}`}
            className='flex min-w-0 flex-wrap items-baseline justify-between gap-x-4 gap-y-1 text-sm'
          >
            <span className='min-w-0 break-words'>
              {platformTitle(quote.platform, t)} / {quote.group}
              {quote.models && (
                <span className='text-muted-foreground'> · {quote.models}</span>
              )}
            </span>
            <span className='shrink-0 tabular-nums'>×{quote.rate}</span>
            {quote.description && (
              <span className='text-muted-foreground w-full text-xs whitespace-pre-wrap'>
                {quote.description}
              </span>
            )}
          </li>
        ))}
      </ul>
    </article>
  )
}

export function ApplicationHistory(props: { owner: number }) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [page, setPage] = useState(1)
  const [cancelTarget, setCancelTarget] = useState<PartnerApplication | null>(
    null
  )
  const query = useQuery({
    ...privateRead,
    queryKey: [...partnerKey, props.owner, 'history', page],
    queryFn: ({ signal }) =>
      listPartnerApplications({
        admin: false,
        page,
        status: '',
        signal,
      }),
  })
  const cancel = useMutation({
    gcTime: 0,
    mutationFn: (application: PartnerApplication) =>
      cancelPartnerApplication(application.id, application.revision),
    onSuccess: () => {
      setCancelTarget(null)
      void client.invalidateQueries({ queryKey: partnerKey })
      toast.success(t('Partner application cancelled.'))
    },
    onError: (error) => handleServerError(error),
    meta: { errorToast: false },
  })

  return (
    <Card className='min-w-0'>
      <CardHeader>
        <CardTitle>{t('My submissions')}</CardTitle>
      </CardHeader>
      <CardContent>
        {query.isPending && <LoadingState className='min-h-24' />}
        {query.isError && (
          <ErrorState
            className='min-h-32'
            title={t('Unable to load partner applications')}
            onRetry={() => void query.refetch()}
          />
        )}
        {query.data && (
          <>
            {query.data.items.length ? (
              <div>
                {query.data.items.map((application) => (
                  <ApplicationRow
                    key={application.id}
                    application={application}
                    onCancel={setCancelTarget}
                  />
                ))}
              </div>
            ) : (
              <EmptyState
                className='min-h-40'
                title={t('No partner applications yet')}
              />
            )}
            {query.data.total > PARTNER_PAGE_SIZE && (
              <SimpleRecordPager
                page={page}
                total={query.data.total}
                pageSize={PARTNER_PAGE_SIZE}
                onChange={setPage}
              />
            )}
          </>
        )}
      </CardContent>
      <ConfirmDialog
        open={cancelTarget !== null}
        onOpenChange={(open) => {
          if (!open && !cancel.isPending) setCancelTarget(null)
        }}
        title={t('Cancel this partner application?')}
        desc={t(
          'This submission will be marked cancelled. You can submit a corrected application afterward.'
        )}
        confirmText={t('Cancel partner application')}
        cancelBtnText={t('Keep application')}
        destructive
        isLoading={cancel.isPending}
        handleConfirm={() => {
          if (cancelTarget) cancel.mutate(cancelTarget)
        }}
      />
    </Card>
  )
}
