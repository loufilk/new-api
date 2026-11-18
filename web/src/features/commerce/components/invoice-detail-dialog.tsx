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
import { ExternalLink, FileCheck2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'

import type { ApplicationReview, InvoiceApplication } from '../types'
import { ApplicationReviewDialog } from './application-review-dialog'
import {
  ApplicationStatus,
  ApplicationTimeline,
  InvoiceMoney,
} from './application-status'

export function InvoiceDetailDialog(props: {
  invoice: InvoiceApplication
  admin: boolean
  canReview: boolean
  pending: boolean
  onClose: () => void
  onReview: (input: ApplicationReview) => Promise<void>
}) {
  const { t } = useTranslation()
  const [action, setAction] = useState<ApplicationReview['action'] | null>(null)
  const row = props.invoice
  const canRecordFee =
    BigInt(row.fee_minor) > 0n &&
    !row.fee_receipt_reference &&
    ['awaiting_fee', 'cancelled', 'rejected'].includes(row.status)
  return (
    <>
      <Dialog
        open
        onOpenChange={(open) => {
          if (!open && !props.pending) props.onClose()
        }}
        title={t('Invoice application #{{id}}', { id: row.id })}
        contentClassName='sm:max-w-3xl'
        footer={
          <div className='flex w-full flex-wrap items-center justify-end gap-2'>
            {!props.admin &&
              (row.status === 'submitted' || row.status === 'awaiting_fee') && (
                <Button variant='outline' onClick={() => setAction('cancel')}>
                  {t('Cancel application')}
                </Button>
              )}
            {props.canReview && canRecordFee && (
              <Button onClick={() => setAction('confirm_fee')}>
                {t('Record service fee receipt')}
              </Button>
            )}
            {props.canReview && row.fee_refund_status === 'pending' && (
              <Button onClick={() => setAction('record_fee_refund')}>
                {t('Record service fee refund')}
              </Button>
            )}
            {props.canReview && row.status === 'submitted' && (
              <Button onClick={() => setAction('approve')}>
                {t('Approve')}
              </Button>
            )}
            {props.canReview && row.status === 'approved' && (
              <Button onClick={() => setAction('issue')}>
                <FileCheck2 aria-hidden />
                {t('Deliver invoice')}
              </Button>
            )}
            {props.canReview &&
              ['submitted', 'awaiting_fee', 'approved'].includes(
                row.status
              ) && (
                <Button
                  variant='destructive'
                  onClick={() => setAction('reject')}
                >
                  {t('Reject')}
                </Button>
              )}
            <Button variant='outline' onClick={props.onClose}>
              {t('Close')}
            </Button>
          </div>
        }
      >
        <div className='space-y-6'>
          {row.fee_refund_status && (
            <p className='rounded-xl border p-3 text-sm'>
              {row.fee_refund_status === 'pending'
                ? t('Service fee refund pending')
                : t('Service fee refunded')}
              {row.fee_refund_reference && ` · ${row.fee_refund_reference}`}
            </p>
          )}
          <div className='bg-muted/30 flex flex-wrap items-start justify-between gap-4 rounded-xl border p-4'>
            <div>
              <ApplicationStatus status={row.status} />
              <h3 className='mt-3 text-lg font-semibold break-words'>
                {row.title}
              </h3>
              <p className='text-muted-foreground mt-1 text-xs'>
                {row.kind === 'special'
                  ? t('Special invoice')
                  : t('General invoice')}{' '}
                ·{' '}
                {row.title_type === 'company' ? t('Company') : t('Individual')}
              </p>
            </div>
            <div className='text-right'>
              <p className='text-muted-foreground text-xs'>
                {t('Invoice amount')}
              </p>
              <p className='mt-1 text-xl font-semibold'>
                <InvoiceMoney
                  minor={row.amount_minor}
                  currency={row.currency}
                  exponent={row.currency_exponent}
                />
              </p>
              <p className='text-muted-foreground mt-2 text-xs'>
                {t('Service fee')}:{' '}
                <InvoiceMoney
                  minor={row.fee_minor}
                  currency={row.currency}
                  exponent={row.currency_exponent}
                />
              </p>
            </div>
          </div>
          <dl className='grid gap-x-6 gap-y-4 text-sm sm:grid-cols-2'>
            {[
              [t('Delivery email'), row.email],
              [t('Tax identification number'), row.tax_id],
              [t('Registered address'), row.address],
              [t('Registered phone'), row.phone],
              [t('Bank name'), row.bank_name],
              [t('Bank account'), row.bank_account],
              [t('Invoice number'), row.invoice_number],
              [t('Payment receipt reference'), row.fee_receipt_reference],
            ]
              .filter(([, value]) => value)
              .map(([label, value]) => (
                <div key={label}>
                  <dt className='text-muted-foreground text-xs'>{label}</dt>
                  <dd className='mt-1 break-all'>{value}</dd>
                </div>
              ))}
          </dl>
          {row.review_note && (
            <div className='rounded-xl border p-4'>
              <h4 className='text-sm font-medium'>{t('Review note')}</h4>
              <p className='text-muted-foreground mt-2 text-sm break-words whitespace-pre-wrap'>
                {row.review_note}
              </p>
            </div>
          )}
          {row.status === 'issued' &&
            row.delivery_url.startsWith('https://') && (
              <Button
                render={
                  <a
                    href={row.delivery_url}
                    target='_blank'
                    rel='noopener noreferrer'
                  />
                }
              >
                {t('Open issued invoice')}
                <ExternalLink aria-hidden />
              </Button>
            )}
          <section>
            <h4 className='mb-3 text-sm font-semibold'>
              {t('Included orders')}
            </h4>
            <StaticDataTable
              data={row.orders ?? []}
              getRowKey={(order) => order.topup_id}
              columns={[
                {
                  id: 'order',
                  header: t('Order number'),
                  cell: (order) => order.trade_no,
                },
                {
                  id: 'amount',
                  header: t('Payment amount'),
                  cell: (order) => (
                    <InvoiceMoney
                      minor={order.amount_minor}
                      currency={order.currency}
                      exponent={order.currency_exponent}
                    />
                  ),
                },
              ]}
            />
          </section>
          <ApplicationTimeline events={row.events ?? []} />
        </div>
      </Dialog>
      {action && (
        <ApplicationReviewDialog
          key={`${row.id}:${action}`}
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
