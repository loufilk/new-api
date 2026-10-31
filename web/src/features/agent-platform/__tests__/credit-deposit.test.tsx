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
import { fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { CreditPanel } from '../components/credit'
import { DepositPanel } from '../components/deposits'
import {
  mountExpansion,
  ok,
  page,
  setupExpansionUser,
} from './expansion-fixtures'

const credit = {
  account: null,
  limits: [],
  limit_requests: [],
  draws: [],
  receivables: [],
  invoices: [],
  repayments: [],
  adjustments: [],
  ledger: [],
  has_more: false,
}
const deposit = {
  account: {
    agent_user_id: 8,
    currency: 'USD',
    currency_exponent: 2,
    available_minor: '0',
    held_minor: '9007199254740993',
  },
  receipts: [],
  requests: [
    {
      id: 3,
      agent_user_id: 8,
      deposit_id: 7,
      kind: 'refund',
      state: 'payment_unknown',
      version: 4,
      business_key: 'deposit-refund:3',
      amount_minor: '9007199254740993',
      currency: 'USD',
      currency_exponent: 2,
      submitted_by: 43,
    },
  ],
  ledger: [],
  has_more: false,
}
async function selectTarget() {
  await userEvent.click(
    await screen.findByRole('button', { name: 'Find user' })
  )
  fireEvent.change(screen.getByRole('textbox', { name: 'User ID' }), {
    target: { value: '8' },
  })
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
}
beforeEach(() => setupExpansionUser())
describe('credit and deposit workflow safeguards', () => {
  test('default-off credit leaves history visible but disables new applications', async () => {
    vi.mocked(api.get).mockImplementation(async (url) =>
      ok(String(url).endsWith('/credit') ? credit : { credit_enabled: false })
    )
    await mountExpansion(<CreditPanel salesEnabled settlementEnabled />)
    expect(
      await screen.findByRole('button', { name: 'Apply for credit limit' })
    ).toBeDisabled()
    expect(
      screen.getByText('No credit account has been established.')
    ).toBeInTheDocument()
  })
  test('a zero replacement credit limit must be explicitly entered and remains a separately approved application', async () => {
    vi.mocked(api.get).mockImplementation(async (url) =>
      ok(
        String(url).endsWith('/credit')
          ? credit
          : { credit_enabled: true, credit_terms_approved: true }
      )
    )
    await mountExpansion(<CreditPanel salesEnabled settlementEnabled />)
    await userEvent.click(
      await screen.findByRole('button', { name: 'Apply for credit limit' })
    )
    fireEvent.change(
      await screen.findByRole('textbox', { name: 'Amount (minor units)' }),
      { target: { value: '0' } }
    )
    fireEvent.change(
      screen.getByRole('textbox', { name: 'Credit term (seconds)' }),
      { target: { value: '86400' } }
    )
    fireEvent.change(screen.getByLabelText('Expires at'), {
      target: { value: '2030-01-01T12:00' },
    })
    await userEvent.click(
      screen.getByRole('checkbox', { name: 'Freeze new credit' })
    )
    fireEvent.change(screen.getByRole('textbox', { name: 'Reason' }), {
      target: { value: 'Explicitly close new credit exposure' },
    })
    fireEvent.change(
      screen.getByRole('textbox', { name: 'Controlled evidence dossier ID' }),
      { target: { value: 'dossier:limit-close-01' } }
    )
    await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
    await screen.findByRole('button', { name: 'Authorize transaction' })
    expect(vi.mocked(api.request).mock.calls[0][0].data).toMatchObject({
      action: 'credit.limit.request',
      payload: { amount_minor: '0', frozen: true, expected_limit_id: 0 },
    })
    expect(api.request).toHaveBeenCalledTimes(1)
  })
  test('finance cannot query a credit account until an explicit target is selected', async () => {
    vi.mocked(api.get).mockResolvedValue(ok(credit))
    await mountExpansion(<CreditPanel finance />)
    expect(api.get).not.toHaveBeenCalled()
    await selectTarget()
    await screen.findByText('No credit account has been established.')
    expect(api.get).toHaveBeenCalledWith(
      '/api/agent-platform/credit',
      expect.objectContaining({
        params: expect.objectContaining({
          agent_user_id: 8,
          offset: 0,
          limit: 100,
        }),
      })
    )
  })
  test('UNKNOWN deposit results expose reconciliation but never a new payment submission', async () => {
    vi.mocked(api.get).mockImplementation(async (url) =>
      ok(String(url).endsWith('/deposit') ? deposit : {})
    )
    await mountExpansion(<DepositPanel finance />)
    await selectTarget()
    await userEvent.click(
      await screen.findByRole('tab', { name: 'Deposit requests' })
    )
    expect(
      await screen.findByRole('button', {
        name: 'Reconcile deposit payment result',
      })
    ).toBeEnabled()
    expect(
      screen.queryByRole('button', { name: 'Record offline deposit payment' })
    ).not.toBeInTheDocument()
    expect(screen.getByText('deposit-refund:3')).toBeInTheDocument()
    expect(
      screen.getAllByText(/90,071,992,547,409\.93/).length
    ).toBeGreaterThan(0)
  })
  test('UNKNOWN reconciliation binds the original business number and omits successful-payment fields', async () => {
    vi.mocked(api.get).mockImplementation(async (url) =>
      ok(String(url).endsWith('/deposit') ? deposit : {})
    )
    await mountExpansion(<DepositPanel finance />)
    await selectTarget()
    await userEvent.click(
      await screen.findByRole('tab', { name: 'Deposit requests' })
    )
    await userEvent.click(
      await screen.findByRole('button', {
        name: 'Reconcile deposit payment result',
      })
    )
    fireEvent.change(
      await screen.findByRole('combobox', { name: 'Actual payment result' }),
      { target: { value: 'unknown' } }
    )
    fireEvent.change(
      screen.getByRole('textbox', { name: 'Controlled evidence dossier ID' }),
      { target: { value: 'dossier:review-003' } }
    )
    await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
    await screen.findByRole('button', { name: 'Authorize transaction' })
    const body = vi.mocked(api.request).mock.calls[0][0].data as {
      action: string
      payload: unknown
    }
    expect(body).toMatchObject({
      action: 'deposit.refund.resolve',
      payload: {
        resource_id: 3,
        version: 4,
        business_key: 'deposit-refund:3',
        outcome: 'unknown',
        evidence_reference: 'dossier:review-003',
      },
    })
    expect(body.payload).not.toHaveProperty('provider')
    expect(body.payload).not.toHaveProperty('no_payment_confirmed')
    expect(body.payload).not.toHaveProperty('paid_at')
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(
      await screen.findByRole('combobox', { name: 'Actual payment result' })
    ).toHaveValue('unknown')
    expect(api.request).toHaveBeenCalledTimes(1)
  })
  test('the offline payment submitter cannot independently resolve their own deposit payout', async () => {
    vi.mocked(api.get).mockImplementation(async (url) =>
      ok(
        String(url).endsWith('/deposit')
          ? {
              ...deposit,
              requests: [{ ...deposit.requests[0], submitted_by: 42 }],
            }
          : page([])
      )
    )
    await mountExpansion(<DepositPanel finance />)
    await selectTarget()
    await userEvent.click(
      await screen.findByRole('tab', { name: 'Deposit requests' })
    )
    await waitFor(() =>
      expect(screen.getByText('deposit-refund:3')).toBeInTheDocument()
    )
    expect(
      screen.queryByRole('button', { name: 'Reconcile deposit payment result' })
    ).not.toBeInTheDocument()
  })
})
