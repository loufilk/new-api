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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { CreateWithdrawalDialog } from '../create-dialog'
import { WithdrawalDetailDialog } from '../detail-dialog'
import type { Withdrawal, WithdrawalSummary } from '../types'

const verification = vi.hoisted(() => ({ requestVerification: vi.fn() }))
vi.mock('@/features/auth/secure-verification', () => ({
  useSecureVerification: () => ({
    ...verification,
    isActive: false,
    dialogProps: {},
  }),
  SecureVerificationDialog: () => null,
}))

const summary: WithdrawalSummary = {
  config: {
    revision: 2,
    enabled: true,
    min_consumed_quota: 0,
    min_withdrawal_quota: 500000,
    quota_per_usdt: 500000,
    bsc_fee_bps: 100,
    polygon_fee_bps: 0,
    trc20_fee_bps: 0,
    instructions: '',
    updated_at: 0,
    updated_by: 1,
  },
  available_quota: 5000000,
  reserved_quota: 0,
  consumed_quota: 0,
  eligible: true,
}
const quote: Withdrawal = {
  id: 15,
  user_id: 8,
  amount_quota: 500000,
  policy_revision: 2,
  network: 'BSC',
  address: '0x1111111111111111111111111111111111111111',
  quota_per_usdt: 500000,
  gross_usdt_micro: '1000000',
  fee_usdt_micro: '10000',
  net_usdt_micro: '990000',
  fee_bps: 100,
  status: 'pending',
  revision: 1,
  transaction_hash: '',
  review_note: '',
  created_at: 100,
  updated_at: 100,
  paid_at: 0,
  events: [],
}

function renderWithQueries(node: React.ReactNode) {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  })
  return render(
    <QueryClientProvider client={client}>{node}</QueryClientProvider>
  )
}

beforeEach(() => {
  useAuthStore.getState().auth.setUser({ id: 8, username: 'inviter', role: 1 })
  verification.requestVerification
    .mockReset()
    .mockResolvedValue({ proof_token: 'operation-proof' })
})

test('quotes the destination and amount before asking for transaction-bound verification and preserves its idempotency key after an uncertain response', async () => {
  const completed = vi.fn()
  let writes = 0
  const post = vi.spyOn(api, 'post').mockImplementation(async (url) => {
    if (url === '/api/referral-withdrawals/quote') {
      return { data: { success: true, data: quote } }
    }
    writes++
    if (writes === 1) throw new Error('Uncertain response')
    return { data: { success: true, data: quote } }
  })
  const user = userEvent.setup()
  renderWithQueries(
    <CreateWithdrawalDialog
      summary={summary}
      onClose={vi.fn()}
      onCreated={completed}
    />
  )
  await user.type(screen.getByLabelText('Receiving address'), quote.address)
  await user.click(screen.getByRole('button', { name: 'Review withdrawal' }))
  expect(await screen.findByText('You receive')).toBeVisible()
  expect(post).toHaveBeenCalledTimes(1)
  expect(verification.requestVerification).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Confirm withdrawal' }))
  await waitFor(() => expect(writes).toBe(1))
  expect(completed).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Confirm withdrawal' }))
  await waitFor(() => expect(completed).toHaveBeenCalledWith(quote))
  const submissions = post.mock.calls.filter(
    ([url]) => url === '/api/referral-withdrawals'
  )
  expect(submissions[0][2]).toEqual(submissions[1][2])
  expect(verification.requestVerification).toHaveBeenCalledWith(
    expect.objectContaining({
      scope: 'referral.withdrawal.create',
      context: {
        request_key: expect.any(String),
        input: {
          amount_quota: 500000,
          network: 'BSC',
          address: quote.address,
          policy_revision: 2,
        },
      },
    })
  )
  expect(submissions[0][2]).toMatchObject({
    headers: { 'X-Security-Proof': 'operation-proof' },
  })
})

test('payment processing hides release actions and requires a transaction hash before completion', async () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 90, username: 'operator', role: 100 })
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: { ...quote, status: 'paid', revision: 4 } },
  })
  const changed = vi.fn()
  const user = userEvent.setup()
  renderWithQueries(
    <WithdrawalDetailDialog
      row={{ ...quote, status: 'paying', revision: 3 }}
      admin
      onClose={vi.fn()}
      onChanged={changed}
    />
  )
  expect(
    screen.queryByRole('button', { name: 'Reject withdrawal' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Cancel withdrawal' })
  ).not.toBeInTheDocument()
  await user.click(
    screen.getByRole('button', { name: 'Record completed payment' })
  )
  expect(
    screen.getByRole('button', { name: 'Record completed payment' })
  ).toBeDisabled()
  await user.type(
    screen.getByLabelText('Transaction hash'),
    `0x${'a'.repeat(64)}`
  )
  await user.click(
    screen.getByRole('button', { name: 'Record completed payment' })
  )
  await waitFor(() => expect(changed).toHaveBeenCalledTimes(1))
  expect(post).toHaveBeenCalledWith(
    '/api/admin/referral-withdrawals/15/pay',
    expect.objectContaining({
      action: 'paid',
      expected_revision: 3,
      transaction_hash: `0x${'a'.repeat(64)}`,
    }),
    expect.any(Object)
  )
})

test('administrators cannot act on their own payout request', () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 8, username: 'operator', role: 100 })
  renderWithQueries(
    <WithdrawalDetailDialog
      row={quote}
      admin
      onClose={vi.fn()}
      onChanged={vi.fn()}
    />
  )
  expect(
    screen.queryByRole('button', { name: 'Approve withdrawal' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Reject withdrawal' })
  ).not.toBeInTheDocument()
})
