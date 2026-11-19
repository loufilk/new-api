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
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { formatTimestamp } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { downloadInvoiceDocument } from '../contracts'
import { invoiceAPI, invoiceKey, privateRead } from './api'
import {
  useInvoiceText,
  invoiceTypeLabels,
  titleTypeLabels,
  categoryLabels,
  applicationStatusLabel,
  type InvoiceTextKey,
} from './copy'

export function ApplicationDetail(props: {
  owner: number
  id: number
  onClose: () => void
}) {
  const { text, money } = useInvoiceText()
  const client = useQueryClient()
  const [confirming, setConfirming] = useState(false)
  const pendingDownload = useRef<AbortController | null>(null)
  useEffect(() => () => pendingDownload.current?.abort(), [])
  const detail = useQuery({
    ...privateRead,
    queryKey: [...invoiceKey(props.owner), 'detail', props.id],
    queryFn: ({ signal }) => invoiceAPI.detail(props.id, signal),
  })
  const cancel = useMutation({
    mutationFn: () => invoiceAPI.cancel(props.id),
    gcTime: 0,
    onSuccess: () => {
      props.onClose()
      toast.success(text('Application cancelled.'))
    },
    onSettled: () => {
      setConfirming(false)
      void client.invalidateQueries({ queryKey: invoiceKey(props.owner) })
    },
  })
  const download = useMutation({
    mutationFn: async () => {
      pendingDownload.current?.abort()
      const controller = new AbortController()
      pendingDownload.current = controller
      try {
        await downloadInvoiceDocument(props.id, false, controller.signal)
      } finally {
        if (pendingDownload.current === controller) {
          pendingDownload.current = null
        }
      }
    },
    gcTime: 0,
    meta: { errorToast: false },
  })
  const data = detail.data
  const canCancel =
    detail.isSuccess &&
    !detail.isFetching &&
    data?.status === 'pending' &&
    !data.processing
  let rows: [InvoiceTextKey, string | undefined][] = []
  if (data) {
    rows = [
      ['Application status', text(applicationStatusLabel(data))],
      ['Invoice amount', money(data.amount_minor)],
      ['Submitted at', formatTimestamp(data.created_at)],
      ['Invoice type', text(invoiceTypeLabels[data.invoice_type])],
      ['Title type', text(titleTypeLabels[data.title_type])],
      ['Invoice title', data.title],
      ['Tax ID', data.tax_id],
      ['Invoice category', text(categoryLabels[data.category])],
      ['Email address', data.email],
      ['Registered address', data.address],
      ['Registered phone', data.phone],
      ['Bank name', data.bank_name],
      ['Bank account number', data.bank_account],
      ['Rejection reason', data.reason],
      ['Invoice number', data.invoice_number],
      [
        'Issued at',
        data.issued_at ? formatTimestamp(data.issued_at) : undefined,
      ],
    ]
  }
  return (
    <>
      <Dialog
        open
        onOpenChange={(open) => {
          if (!open && !(confirming && (canCancel || cancel.isPending))) {
            props.onClose()
          }
        }}
        title={`${text('Application details')} #${props.id}`}
        footer={
          <>
            {canCancel && (
              <Button
                variant='outline'
                onClick={() => setConfirming(true)}
                disabled={cancel.isPending}
              >
                {text('Cancel application')}
              </Button>
            )}
            {data?.status === 'issued' && data.has_document && (
              <Button
                onClick={() => download.mutate()}
                disabled={download.isPending}
              >
                {text('Download PDF')}
              </Button>
            )}
          </>
        }
      >
        {detail.isPending && <LoadingState />}
        {detail.isError && (
          <ErrorState
            title={text('Unable to load invoice information')}
            onRetry={() => void detail.refetch()}
          />
        )}
        {data && (
          <>
            <dl className='divide-y text-sm'>
              {rows
                .filter(([, value]) => !!value)
                .map(([label, value]) => (
                  <div
                    key={label}
                    className='grid grid-cols-[minmax(6rem,1fr)_2fr] gap-3 py-2'
                  >
                    <dt className='text-muted-foreground'>{text(label)}</dt>
                    <dd className='break-all'>{value}</dd>
                  </div>
                ))}
            </dl>
            <h3 className='mt-4 font-medium'>{text('Included orders')}</h3>
            <ul className='mt-2 divide-y rounded-lg border px-3'>
              {(data.orders || []).map((order) => (
                <li
                  key={order.id}
                  className='flex flex-wrap justify-between gap-2 py-2 text-xs'
                >
                  <div className='min-w-0'>
                    <p className='font-mono break-all'>{order.reference}</p>
                    <p className='text-muted-foreground mt-1'>
                      {formatTimestamp(order.paid_at)}
                    </p>
                  </div>
                  <span className='tabular-nums'>
                    {money(order.amount_minor)}
                  </span>
                </li>
              ))}
            </ul>
          </>
        )}
        {download.isError && (
          <p role='alert' className='text-destructive mt-2 text-sm'>
            {getServerErrorMessage(download.error)}
          </p>
        )}
      </Dialog>
      <ConfirmDialog
        open={confirming && (canCancel || cancel.isPending)}
        onOpenChange={(open) => {
          if (!cancel.isPending) setConfirming(open)
        }}
        title={text('Cancel this application?')}
        desc={text(
          'Eligible orders in this application will become available again.'
        )}
        cancelBtnText={text('Keep application')}
        confirmText={text('Cancel application')}
        isLoading={cancel.isPending}
        disabled={!canCancel}
        handleConfirm={() => {
          if (!cancel.isPending && canCancel) cancel.mutate()
        }}
      />
    </>
  )
}
