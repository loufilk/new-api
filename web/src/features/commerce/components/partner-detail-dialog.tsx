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
import { ExternalLink } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'

import type { ApplicationReview, PartnerApplication } from '../types'
import { ApplicationReviewDialog } from './application-review-dialog'
import { ApplicationStatus, ApplicationTimeline } from './application-status'

export function PartnerDetailDialog(props: {
  application: PartnerApplication
  admin: boolean
  canReview: boolean
  pending: boolean
  onClose: () => void
  onReview: (input: ApplicationReview) => Promise<void>
}) {
  const { t } = useTranslation()
  const [action, setAction] = useState<ApplicationReview['action'] | null>(null)
  const row = props.application
  const active = row.status === 'submitted' || row.status === 'reviewing'
  return (
    <>
      <Dialog
        open
        onOpenChange={(open) => {
          if (!open && !props.pending) props.onClose()
        }}
        title={t('Partner application #{{id}}', { id: row.id })}
        contentClassName='sm:max-w-4xl'
        footer={
          <>
            {!props.admin && active && (
              <Button variant='outline' onClick={() => setAction('cancel')}>
                {t('Cancel application')}
              </Button>
            )}
            {props.canReview && row.status === 'submitted' && (
              <Button variant='outline' onClick={() => setAction('review')}>
                {t('Start review')}
              </Button>
            )}
            {props.canReview && active && (
              <>
                <Button onClick={() => setAction('approve')}>
                  {t('Approve')}
                </Button>
                <Button
                  variant='destructive'
                  onClick={() => setAction('reject')}
                >
                  {t('Reject')}
                </Button>
              </>
            )}
            <Button variant='outline' onClick={props.onClose}>
              {t('Close')}
            </Button>
          </>
        }
      >
        <div className='space-y-6'>
          <div className='bg-muted/30 flex flex-wrap items-start justify-between gap-3 rounded-xl border p-4'>
            <div>
              <h3 className='text-lg font-semibold'>{row.site_name}</h3>
              <Button
                className='mt-2 h-auto p-0'
                variant='link'
                render={
                  <a
                    href={row.site_url}
                    target='_blank'
                    rel='noopener noreferrer'
                  />
                }
              >
                {t('Visit partner website')}
                <ExternalLink className='size-3.5' aria-hidden />
              </Button>
            </div>
            <ApplicationStatus status={row.status} />
          </div>
          <dl className='grid gap-4 sm:grid-cols-3'>
            {[
              [t('Contact name'), row.contact_name],
              [t('Contact email'), row.contact_email],
              [t('Other contact method'), row.contact_method],
            ].map(([label, value]) => (
              <div key={label}>
                <dt className='text-muted-foreground text-xs'>{label}</dt>
                <dd className='mt-1 text-sm break-all'>{value || '—'}</dd>
              </div>
            ))}
          </dl>
          <section>
            <h4 className='mb-2 text-sm font-semibold'>
              {t('Supply and service description')}
            </h4>
            <p className='text-muted-foreground text-sm break-words whitespace-pre-wrap'>
              {row.description}
            </p>
          </section>
          <section>
            <h4 className='mb-3 text-sm font-semibold'>{t('Group quotes')}</h4>
            <StaticDataTable
              data={row.quotes ?? []}
              getRowKey={(quote) => quote.group}
              columns={[
                {
                  id: 'group',
                  header: t('Group name'),
                  cell: (quote) => quote.group,
                },
                {
                  id: 'models',
                  header: t('Supported models'),
                  cell: (quote) => quote.models,
                },
                {
                  id: 'rate',
                  header: t('Quoted multiplier'),
                  cell: (quote) => `${quote.rate}×`,
                },
                {
                  id: 'notes',
                  header: t('Service notes'),
                  cell: (quote) => quote.description || '—',
                },
              ]}
            />
          </section>
          {row.review_note && (
            <div className='rounded-xl border p-4'>
              <h4 className='text-sm font-semibold'>{t('Review note')}</h4>
              <p className='text-muted-foreground mt-2 text-sm break-words whitespace-pre-wrap'>
                {row.review_note}
              </p>
            </div>
          )}
          <ApplicationTimeline events={row.events ?? []} />
        </div>
      </Dialog>
      {action && (
        <ApplicationReviewDialog
          key={`${row.id}:${action}`}
          partner
          action={action}
          revision={row.revision}
          pending={props.pending}
          onClose={() => setAction(null)}
          onSubmit={props.onReview}
        />
      )}
    </>
  )
}
