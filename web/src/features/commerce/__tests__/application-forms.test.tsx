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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, test, vi } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'

import { ApplicationReviewDialog } from '../components/application-review-dialog'
import { InvoiceCreateDialog } from '../components/invoice-create-dialog'
import { InvoiceDetailDialog } from '../components/invoice-detail-dialog'
import { PartnerCreateDialog } from '../components/partner-create-dialog'
import type { CommerceConfig, InvoiceApplication } from '../types'

const config: CommerceConfig = {
  revision: 1,
  invoice_enabled: true,
  invoice_window_days: 30,
  invoice_max_orders: 100,
  invoice_currency: 'CNY',
  general_fee_bps: 400,
  special_invoice_enabled: true,
  special_fee_bps: 600,
  invoice_instructions: '',
  partner_enabled: true,
  partner_instructions: '',
  updated_at: 0,
  updated_by: 0,
}

afterEach(() => useAuthStore.getState().auth.reset())

describe('invoice application form', () => {
  test('shows the eligible-order empty state and disables submission', () => {
    render(
      <InvoiceCreateDialog
        config={config}
        orders={[]}
        pending={false}
        onClose={vi.fn()}
        onSubmit={vi.fn()}
      />
    )
    expect(screen.getByText('No eligible orders')).toBeVisible()
    expect(
      screen.getByRole('button', { name: 'Submit application' })
    ).toBeDisabled()
  })

  test('preserves the draft and idempotency key when an uncertain submission is retried', async () => {
    useAuthStore.getState().auth.setUser({
      id: 21,
      username: 'buyer',
      role: 1,
      email: 'buyer@example.com',
    })
    const submit = vi.fn().mockRejectedValue(new Error('Uncertain response'))
    const user = userEvent.setup()
    render(
      <InvoiceCreateDialog
        config={config}
        orders={[
          {
            topup_id: 71,
            trade_no: 'ORDER-71',
            currency: 'CNY',
            currency_exponent: 2,
            amount_minor: '12345',
            completed_at: 1,
          },
        ]}
        pending={false}
        onClose={vi.fn()}
        onSubmit={submit}
      />
    )
    await user.click(screen.getByRole('checkbox', { name: /ORDER-71/ }))
    await user.type(screen.getByLabelText('Invoice title'), 'Example Company')
    await user.type(
      screen.getByLabelText('Tax identification number'),
      'DEMO-TAX'
    )
    await user.click(screen.getByRole('button', { name: 'Submit application' }))
    await waitFor(() => expect(submit).toHaveBeenCalledTimes(1))
    expect(screen.getByLabelText('Invoice title')).toHaveValue(
      'Example Company'
    )
    await user.click(screen.getByRole('button', { name: 'Submit application' }))
    await waitFor(() => expect(submit).toHaveBeenCalledTimes(2))
    expect(submit.mock.calls[0][1]).toBe(submit.mock.calls[1][1])
    expect(submit.mock.calls[0][0]).toMatchObject({
      order_ids: [71],
      title: 'Example Company',
      email: 'buyer@example.com',
    })
  })
})

describe('partner quotes form', () => {
  test('keeps one quote and supports adding and removing additional quotes with accessible names', async () => {
    const user = userEvent.setup()
    render(
      <PartnerCreateDialog
        pending={false}
        onClose={vi.fn()}
        onSubmit={vi.fn()}
      />
    )
    expect(
      screen.getByRole('button', { name: 'Remove quote 1' })
    ).toBeDisabled()
    await user.click(screen.getByRole('button', { name: 'Add quote' }))
    expect(screen.getAllByLabelText('Group name')).toHaveLength(2)
    await user.click(screen.getByRole('button', { name: 'Remove quote 2' }))
    expect(screen.getAllByLabelText('Group name')).toHaveLength(1)
  })
})

describe('application review confirmation', () => {
  test('requires a review note before rejecting an application', async () => {
    const submit = vi.fn().mockResolvedValue(undefined)
    const user = userEvent.setup()
    render(
      <ApplicationReviewDialog
        action='reject'
        revision={7}
        pending={false}
        onClose={vi.fn()}
        onSubmit={submit}
      />
    )
    await user.click(screen.getByRole('button', { name: 'Confirm' }))
    expect(submit).not.toHaveBeenCalled()
    expect(screen.getByRole('alert')).toHaveTextContent('Enter a review note')
    expect(screen.getByLabelText('Review note')).toHaveAttribute(
      'aria-invalid',
      'true'
    )
    await user.type(
      screen.getByLabelText('Review note'),
      'Please correct your tax ID'
    )
    await user.click(screen.getByRole('button', { name: 'Confirm' }))
    await waitFor(() =>
      expect(submit).toHaveBeenCalledWith(
        expect.objectContaining({
          action: 'reject',
          expected_revision: 7,
          note: 'Please correct your tax ID',
        })
      )
    )
  })
  test('rejects a non-HTTPS delivery URL before issuing an invoice', async () => {
    const submit = vi.fn()
    const user = userEvent.setup()
    render(
      <ApplicationReviewDialog
        action='issue'
        revision={3}
        pending={false}
        onClose={vi.fn()}
        onSubmit={submit}
      />
    )
    await user.type(screen.getByLabelText('Invoice number'), 'INV-001')
    await user.type(
      screen.getByLabelText('Invoice delivery URL'),
      'javascript:alert(1)'
    )
    await user.click(screen.getByRole('button', { name: 'Confirm' }))
    expect(submit).not.toHaveBeenCalled()
    expect(screen.getByRole('alert')).toHaveTextContent(
      'Enter the invoice number and a valid HTTPS delivery URL'
    )
  })

  test('an administrator can record a late service fee on a closed invoice while its rejection reason stays visible', async () => {
    const invoice: InvoiceApplication = {
      id: 15,
      user_id: 21,
      kind: 'general',
      title_type: 'company',
      title: 'Example Company',
      tax_id: 'DEMO-TAX',
      email: 'buyer@example.com',
      address: '',
      phone: '',
      bank_name: '',
      bank_account: '',
      currency: 'CNY',
      currency_exponent: 2,
      amount_minor: '10000',
      fee_minor: '100',
      fee_bps: 100,
      fee_receipt_reference: '',
      fee_refund_status: '',
      fee_refund_reference: '',
      status: 'rejected',
      revision: 2,
      review_note: 'Please correct your tax identifier',
      invoice_number: '',
      delivery_url: '',
      created_at: 1,
      updated_at: 2,
      orders: [],
      events: [
        {
          id: 2,
          action: 'reject',
          from_status: 'awaiting_fee',
          to_status: 'rejected',
          note: 'Please correct your tax identifier',
          created_at: 2,
        },
      ],
    }
    const onReview = vi.fn().mockResolvedValue(undefined)
    const user = userEvent.setup()
    render(
      <InvoiceDetailDialog
        invoice={invoice}
        admin
        canReview
        pending={false}
        onClose={vi.fn()}
        onReview={onReview}
      />
    )
    expect(
      screen.getAllByText('Please correct your tax identifier')
    ).toHaveLength(2)
    await user.click(
      screen.getByRole('button', { name: 'Record service fee receipt' })
    )
    await user.type(
      screen.getByLabelText('Payment receipt reference'),
      'late-manual-receipt'
    )
    await user.click(screen.getByRole('button', { name: 'Confirm' }))
    await waitFor(() =>
      expect(onReview).toHaveBeenCalledWith(
        expect.objectContaining({
          action: 'confirm_fee',
          expected_revision: 2,
          fee_receipt_reference: 'late-manual-receipt',
        })
      )
    )
  })
})
