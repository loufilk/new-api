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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { ActionForm } from '../components/action-form'
import { CommissionTransfer } from '../components/commission-transfer'
import { Commissions } from '../components/commissions'
import { Withdrawals } from '../components/withdrawals'
import { currencyOptions, validCurrencyExponent } from '../lib/currencies'
import type { CommissionAccount } from '../types'

const account: CommissionAccount = {
  id: 1,
  user_id: 42,
  currency: 'USD',
  currency_exponent: 2,
  pending_minor: '100',
  available_minor: '9007199254740991',
  reserved_minor: '300',
  withdrawal_held_minor: '400',
  debt_minor: '500',
  total_earned_minor: '9007199254740991',
  total_reversed_minor: '0',
}
const ok = (data: unknown) => ({ data: { success: true, data } })
const page = (items: unknown[]) => ({
  items,
  total: items.length,
  page: 1,
  page_size: 20,
})
function mount(node: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>{node}</QueryClientProvider>
  )
}
beforeEach(() => {
  sessionStorage.clear()
  useAuthStore.getState().auth.setUser({
    id: 42,
    username: 'ordinary-inviter',
    role: 1,
    agent_profile: null,
  })
  vi.spyOn(api, 'get').mockResolvedValue(ok(page([])))
  vi.spyOn(api, 'request').mockResolvedValue(
    ok({ quote_id: 'immutable-quote' })
  )
})
describe('commission controls preserve source and recipient versions', () => {
  test('ordinary inviters without an agent profile can use server-allowed commission actions; all five buckets are distinct', async () => {
    vi.mocked(api.get).mockImplementation(async (url) =>
      url.endsWith('/summary')
        ? ok({
            accounts: [account],
            legacy_aff_quota: 500000,
            allowed_actions: ['commission.convert.wallet'],
            disabled_reason: '',
          })
        : ok(page([]))
    )
    mount(<Commissions />)
    expect(await screen.findByText('Pending maturity')).toBeInTheDocument()
    expect(screen.getByText('Available commission')).toBeInTheDocument()
    expect(screen.getByText('Risk reserve')).toBeInTheDocument()
    expect(screen.getByText('Withdrawal holds')).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Convert to reward wallet' })
    ).toBeEnabled()
    expect(
      screen.getByRole('button', { name: 'Request withdrawal' })
    ).toBeDisabled()
    expect(
      screen.getByRole('button', { name: 'Convert to procurement quota' })
    ).toBeDisabled()
  })
  test('withdrawal quote binds the current UUID recipient version and exact decimal amount, with the same ID used for execution', async () => {
    const onPrepared = vi.fn()
    vi.mocked(api.get).mockResolvedValue(
      ok(
        page([
          {
            id: 'recipient-uuid',
            version: 1,
            label: 'Old',
            reference_masked: '••1',
          },
          {
            id: 'recipient-uuid',
            version: 3,
            label: 'Approved file',
            reference_masked: '••3',
          },
        ])
      )
    )
    mount(
      <CommissionTransfer
        target='withdrawal'
        accounts={[account]}
        onPrepared={onPrepared}
        onClose={vi.fn()}
      />
    )
    const currency = await screen.findByRole('combobox', {
      name: 'Original currency',
    })
    fireEvent.change(currency, { target: { value: 'USD' } })
    fireEvent.change(
      screen.getByRole('textbox', { name: 'Amount in original currency' }),
      { target: { value: '90071992547409.91' } }
    )
    const recipient = screen.getByRole('combobox', {
      name: 'Payout recipient version',
    })
    expect(
      screen.queryByRole('option', { name: /Old/ })
    ).not.toBeInTheDocument()
    fireEvent.change(recipient, { target: { value: 'recipient-uuid:3' } })
    fireEvent.click(screen.getByRole('button', { name: 'Continue' }))
    await waitFor(() => expect(onPrepared).toHaveBeenCalledOnce())
    expect(api.get).toHaveBeenCalledWith(
      '/api/agent-platform/commission-recipients',
      expect.objectContaining({
        params: expect.objectContaining({ current_only: 'true' }),
      })
    )
    const submitted = vi.mocked(api.request).mock.calls[0][0]
    expect(submitted).toMatchObject({
      url: '/api/agent-platform/withdrawals/quote',
      data: {
        amount_minor: '9007199254740991',
        recipient_id: 'recipient-uuid',
        recipient_version: 3,
        currency: 'USD',
        target: 'withdrawal',
        request_id: expect.any(String),
      },
    })
    expect(onPrepared).toHaveBeenCalledWith(
      expect.objectContaining({
        request_id: (submitted.data as { request_id: string }).request_id,
        payload: { quote_id: 'immutable-quote' },
        endpoint: '/withdrawals',
      })
    )
  })
  test('extra fractional digits are rejected rather than rounded to an authorized amount', async () => {
    mount(
      <CommissionTransfer
        target='wallet'
        accounts={[account]}
        onPrepared={vi.fn()}
        onClose={vi.fn()}
      />
    )
    fireEvent.change(
      screen.getByRole('combobox', { name: 'Original currency' }),
      { target: { value: 'USD' } }
    )
    fireEvent.change(
      screen.getByRole('textbox', { name: 'Amount in original currency' }),
      { target: { value: '1.001' } }
    )
    fireEvent.click(screen.getByRole('button', { name: 'Continue' }))
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Continue' })).toBeEnabled()
    )
    expect(api.request).not.toHaveBeenCalled()
  })
  test('currency selection automatically fixes readonly decimal places and minor-unit strings never pass through Number', async () => {
    const onSubmit = vi.fn()
    mount(
      <ActionForm
        title='Money'
        fields={[
          {
            name: 'currency',
            label: 'Original currency',
            kind: 'select',
            options: currencyOptions('exponent'),
          },
          {
            name: 'exponent',
            label: 'Decimal places',
            kind: 'integer',
            readOnly: true,
          },
          { name: 'amount', label: 'Minor amount', kind: 'minor' },
        ]}
        onSubmit={onSubmit}
        onClose={vi.fn()}
      />
    )
    fireEvent.change(
      screen.getByRole('combobox', { name: 'Original currency' }),
      { target: { value: 'KWD' } }
    )
    expect(screen.getByRole('textbox', { name: 'Decimal places' })).toHaveValue(
      '3'
    )
    expect(
      screen.getByRole('textbox', { name: 'Decimal places' })
    ).toHaveAttribute('readonly')
    fireEvent.change(screen.getByRole('textbox', { name: 'Minor amount' }), {
      target: { value: '9223372036854775807' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Continue' }))
    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith({
        currency: 'KWD',
        exponent: '3',
        amount: '9223372036854775807',
      })
    )
    expect(validCurrencyExponent('USD', 6)).toBe(false)
    expect(validCurrencyExponent('XXX', 2)).toBe(false)
    expect(validCurrencyExponent('JPY', 0)).toBe(true)
  })
  test('manual payout instructions are explicit read-only, original-net matched, and never stored', async () => {
    const withdrawal = {
      id: 4,
      business_no: 'stable-withdrawal-4',
      status: 'unknown',
      user_id: 43,
      currency: 'USD',
      currency_exponent: 2,
      amount_minor: '10000',
      fee_minor: '100',
      net_minor: '9900',
      recipient_label: 'Approved recipient',
      recipient_masked: '••old',
      recipient_version: 2,
      version: 4,
      allowed_actions: [
        'commission.withdraw.resolve',
        'commission.withdraw.instructions',
      ],
    }
    vi.mocked(api.get).mockImplementation(async (url) => {
      if (url.endsWith('/instructions')) {
        return ok({
          business_no: 'stable-withdrawal-4',
          currency: 'USD',
          currency_exponent: 2,
          amount_minor: '9900',
          recipient_registry_id: 'controlled-file-9',
          recipient_label: 'Approved recipient',
          recipient_digest: 'immutable-digest',
          manual_only: true,
        })
      }
      if (url.endsWith('/withdrawals/4')) return ok(withdrawal)
      return ok(page([withdrawal]))
    })
    mount(<Withdrawals finance />)
    fireEvent.click(await screen.findByRole('button', { name: 'View details' }))
    fireEvent.click(
      await screen.findByRole('button', {
        name: 'View manual payment instructions',
      })
    )
    expect(await screen.findByText('controlled-file-9')).toBeInTheDocument()
    expect(screen.getAllByText(/USD.*99\.00/)[0]).toBeInTheDocument()
    expect(sessionStorage.getItem('agent-platform:pending:42')).toBeNull()
    expect(api.request).not.toHaveBeenCalled()
    expect(
      screen.queryByRole('button', { name: 'Record payment submission' })
    ).not.toBeInTheDocument()
  })
})
