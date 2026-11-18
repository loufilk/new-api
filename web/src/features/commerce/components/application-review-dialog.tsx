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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'

import type { ApplicationReview } from '../types'

export function ApplicationReviewDialog(props: {
  action: ApplicationReview['action']
  revision: number
  pending: boolean
  partner?: boolean
  onClose: () => void
  onSubmit: (input: ApplicationReview) => Promise<void>
}) {
  const { t } = useTranslation()
  const [note, setNote] = useState('')
  const [invoiceNumber, setInvoiceNumber] = useState('')
  const [deliveryURL, setDeliveryURL] = useState('')
  const [receipt, setReceipt] = useState('')
  const [error, setError] = useState('')
  const [invalidFields, setInvalidFields] = useState<string[]>([])
  let title = t('Confirm review')
  if (props.action === 'cancel') title = t('Cancel application')
  if (props.action === 'issue') title = t('Deliver invoice')
  if (props.action === 'confirm_fee') title = t('Record service fee receipt')
  if (props.action === 'record_fee_refund') {
    title = t('Record service fee refund')
  }
  let description = t(
    'The applicant will see the updated status and your review note.'
  )
  if (props.action === 'cancel') {
    description = t(
      'Cancel this application? Reserved orders will become eligible again.'
    )
  }
  if (props.action === 'cancel' && props.partner) {
    description = t(
      'Cancel this partnership application? You can submit a new application later.'
    )
  }
  if (props.action === 'confirm_fee') {
    description = t(
      'Confirm only after the service fee has been received. This records the receipt without moving wallet funds.'
    )
  }
  if (props.action === 'record_fee_refund') {
    description = t(
      'Record the refund only after payment has been returned manually.'
    )
  }

  const submit = async () => {
    if (
      (props.action === 'reject' ||
        (props.partner && props.action === 'approve')) &&
      !note.trim()
    ) {
      setError(t('Enter a review note'))
      setInvalidFields(['note'])
      return
    }
    if (
      (props.action === 'confirm_fee' ||
        props.action === 'record_fee_refund') &&
      receipt.trim().length < 3
    ) {
      setError(
        props.action === 'record_fee_refund'
          ? t('Enter the refund receipt reference')
          : t('Enter the payment receipt reference')
      )
      setInvalidFields(['receipt'])
      return
    }
    if (props.action === 'issue') {
      let safe = false
      try {
        const url = new URL(deliveryURL)
        safe = url.protocol === 'https:' && !url.username && !url.password
      } catch {
        /* validation message below */
      }
      if (!invoiceNumber.trim() || !safe) {
        setError(t('Enter the invoice number and a valid HTTPS delivery URL'))
        setInvalidFields([
          ...(!invoiceNumber.trim() ? ['number'] : []),
          ...(!safe ? ['url'] : []),
        ])
        return
      }
    }
    setError('')
    setInvalidFields([])
    try {
      await props.onSubmit({
        expected_revision: props.revision,
        action: props.action,
        note,
        invoice_number: invoiceNumber,
        delivery_url: deliveryURL,
        fee_receipt_reference: receipt,
        fee_refund_reference: receipt,
      })
      props.onClose()
    } catch {
      /* Query mutation presents the server error and preserves this draft. */
    }
  }

  return (
    <ConfirmDialog
      open
      onOpenChange={(open) => {
        if (!open && !props.pending) props.onClose()
      }}
      title={title}
      desc={description}
      confirmText={t('Confirm')}
      destructive={props.action === 'cancel' || props.action === 'reject'}
      isLoading={props.pending}
      handleConfirm={() => void submit()}
    >
      <div className='grid gap-4'>
        {props.action !== 'cancel' && (
          <div className='grid gap-2'>
            <Label htmlFor='commerce-review-note'>{t('Review note')}</Label>
            <Textarea
              id='commerce-review-note'
              value={note}
              aria-invalid={invalidFields.includes('note')}
              maxLength={1000}
              onChange={(event) => setNote(event.target.value)}
            />
          </div>
        )}
        {(props.action === 'confirm_fee' ||
          props.action === 'record_fee_refund') && (
          <div className='grid gap-2'>
            <Label htmlFor='commerce-receipt'>
              {props.action === 'record_fee_refund'
                ? t('Refund receipt reference')
                : t('Payment receipt reference')}
            </Label>
            <Input
              id='commerce-receipt'
              value={receipt}
              aria-invalid={invalidFields.includes('receipt')}
              maxLength={200}
              onChange={(event) => setReceipt(event.target.value)}
            />
          </div>
        )}
        {props.action === 'issue' && (
          <>
            <div className='grid gap-2'>
              <Label htmlFor='commerce-invoice-number'>
                {t('Invoice number')}
              </Label>
              <Input
                id='commerce-invoice-number'
                value={invoiceNumber}
                aria-invalid={invalidFields.includes('number')}
                maxLength={100}
                onChange={(event) => setInvoiceNumber(event.target.value)}
              />
            </div>
            <div className='grid gap-2'>
              <Label htmlFor='commerce-invoice-url'>
                {t('Invoice delivery URL')}
              </Label>
              <Input
                id='commerce-invoice-url'
                type='url'
                value={deliveryURL}
                aria-invalid={invalidFields.includes('url')}
                maxLength={2048}
                placeholder='https://'
                onChange={(event) => setDeliveryURL(event.target.value)}
              />
            </div>
          </>
        )}
        {error && (
          <p role='alert' className='text-destructive text-sm'>
            {error}
          </p>
        )}
      </div>
    </ConfirmDialog>
  )
}
