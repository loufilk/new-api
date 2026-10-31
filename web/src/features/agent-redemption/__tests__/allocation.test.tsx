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
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import {
  authorizeTransaction,
  transactionNetwork,
} from '@/features/redemption-codes/components/__tests__/transaction-fixture'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { AllocationDialog } from '../components/allocation-dialog'
import type { AgentAccount } from '../types'

const account: AgentAccount = {
  active_agent: true,
  agent_user_id: 7,
  available_quota: 500000,
  reserved_quota: 1000000,
  spent_quota: 0,
  allocated_quota: 1500000,
  created_at: 1,
  updated_at: 1,
}
const clients: QueryClient[] = []
function setup(target: AgentAccount = account) {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'operator', role: ROLE.ADMIN })
  useSystemConfigStore.getState().setConfig({
    currency: {
      displayInCurrency: true,
      customCurrencySymbol: '¤',
      customCurrencyExchangeRate: 1,
      quotaDisplayType: 'USD',
      quotaPerUnit: 500000,
      usdExchangeRate: 1,
    },
  })
  const network = transactionNetwork()
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  clients.push(client)
  const onClose = vi.fn()
  render(
    <QueryClientProvider client={client}>
      <AllocationDialog account={target} onClose={onClose} />
    </QueryClientProvider>
  )
  return { onClose, ...network }
}
async function enterAdjustment(amount: string) {
  fireEvent.change(
    screen.getByRole('spinbutton', { name: 'Adjustment (USD)' }),
    { target: { value: amount } }
  )
  fireEvent.change(screen.getByRole('textbox', { name: 'Reason' }), {
    target: { value: 'Monthly allocation' },
  })
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
}
afterEach(() => {
  clients.forEach((client) => client.clear())
  clients.length = 0
  useAuthStore.getState().auth.reset()
  sessionStorage.clear()
  vi.restoreAllMocks()
})

it('prevents reclaiming reserved quota even when the total budget is higher', async () => {
  const network = setup()
  await enterAdjustment('-2')
  expect(
    await screen.findByText(
      'Adjustment exceeds the available quota or safe integer range'
    )
  ).toBeInTheDocument()
  expect(network.prepare).not.toHaveBeenCalled()
  expect(network.mutations).not.toHaveBeenCalled()
})
it('cancelling transaction review sends no allocation and preserves the form', async () => {
  const network = setup()
  await enterAdjustment('1')
  const review = await screen.findByRole('alertdialog', {
    name: 'Review transaction',
  })
  await userEvent.click(within(review).getByRole('button', { name: 'Cancel' }))
  expect(network.verify).not.toHaveBeenCalled()
  expect(network.mutations).not.toHaveBeenCalled()
  expect(
    screen.getByRole('spinbutton', { name: 'Adjustment (USD)' })
  ).toHaveValue(1)
})
it('requires a reason and rejects zero quota before preparing', async () => {
  const network = setup()
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
  expect(
    await screen.findByText('Enter a non-zero quota adjustment')
  ).toBeInTheDocument()
  expect(screen.getByText('Reason is required')).toBeInTheDocument()
  expect(network.prepare).not.toHaveBeenCalled()
})
it('binds the reviewed allocation to MFA, blocks duplicate submissions, and uses the same request ID', async () => {
  const { onClose, mutations, prepare, verify } = setup()
  let finish!: (value: { data: unknown }) => void
  mutations.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        finish = resolve
      })
  )
  await enterAdjustment('1')
  await waitFor(() => expect(prepare).toHaveBeenCalledOnce())
  expect(prepare).toHaveBeenCalledWith(
    expect.objectContaining({
      action: 'redemption.allocate',
      payload: {
        agent_user_id: 7,
        delta: 500000,
        reason: 'Monthly allocation',
      },
    })
  )
  expect(mutations).not.toHaveBeenCalled()
  await authorizeTransaction()
  await waitFor(() => expect(mutations).toHaveBeenCalledOnce())
  const review = screen.getByRole('alertdialog', { name: 'Review transaction' })
  expect(
    within(review).getByRole('button', { name: 'Authorize transaction' })
  ).toBeDisabled()
  expect(within(review).getByRole('button', { name: 'Cancel' })).toBeDisabled()
  await userEvent.keyboard('{Escape}')
  expect(onClose).not.toHaveBeenCalled()
  const requestId = prepare.mock.calls[0][0].request_id
  expect(verify).toHaveBeenCalledWith(
    expect.objectContaining({
      scope: 'agent.transaction',
      context: expect.objectContaining({
        action: 'redemption.allocate',
        request_id: requestId,
        quota: 500000,
      }),
    }),
    expect.anything()
  )
  expect(mutations).toHaveBeenCalledWith(
    expect.objectContaining({
      url: '/api/agent-redemption/allocations',
      method: 'post',
      data: {
        agent_user_id: 7,
        delta: 500000,
        reason: 'Monthly allocation',
        request_id: requestId,
      },
      headers: { 'X-Security-Proof': 'test-single-use-proof' },
      skipAuthRefresh: true,
    })
  )
  finish({ data: { success: true, data: { account, replayed: false } } })
  await waitFor(() => expect(onClose).toHaveBeenCalledOnce())
})
it('keeps an interrupted allocation unresolved and checks status instead of resubmitting', async () => {
  const { mutations, onClose, prepare } = setup()
  mutations.mockRejectedValueOnce(new Error('Offline'))
  const get = vi.spyOn(api, 'get')
  const priorGet = get.getMockImplementation()
  if (!priorGet) throw new Error('Missing network fixture')
  get.mockImplementation((url, config) =>
    url.startsWith('/api/agent-platform/operations/')
      ? Promise.resolve({
          data: {
            success: true,
            data: {
              request_id: url.split('/').at(-1),
              action: 'redemption.allocate',
              status: 'completed',
            },
          },
        })
      : priorGet(url, config)
  )
  await enterAdjustment('1')
  await authorizeTransaction()
  expect(
    await screen.findByText('Do not submit a new request')
  ).toBeInTheDocument()
  expect(
    screen.getByRole('button', { name: 'Authorize transaction' })
  ).toBeDisabled()
  expect(onClose).not.toHaveBeenCalled()
  await userEvent.click(
    screen.getByRole('button', { name: 'Check request status' })
  )
  await waitFor(() => expect(onClose).toHaveBeenCalledOnce())
  expect(mutations).toHaveBeenCalledOnce()
  expect(get).toHaveBeenCalledWith(
    `/api/agent-platform/operations/${prepare.mock.calls[0][0].request_id}`,
    expect.anything()
  )
})
it('does not add new quota to a disabled or downgraded account', async () => {
  const network = setup({
    ...account,
    user_role: ROLE.USER,
    user_status: 2,
    active_agent: false,
  })
  await enterAdjustment('1')
  expect(
    await screen.findByText(
      'Agent account must be active before issuing redemption codes'
    )
  ).toBeInTheDocument()
  expect(network.prepare).not.toHaveBeenCalled()
})
it('allows verified reclamation from a historical account and reviews the negative direction', async () => {
  const { mutations, onClose } = setup({
    ...account,
    user_role: ROLE.USER,
    user_deleted: true,
    active_agent: false,
  })
  await enterAdjustment('-1')
  expect(
    await screen.findByRole('alertdialog', { name: 'Review transaction' })
  ).toHaveTextContent('-')
  await authorizeTransaction()
  await waitFor(() => expect(onClose).toHaveBeenCalledOnce())
  expect(mutations).toHaveBeenCalledWith(
    expect.objectContaining({
      data: expect.objectContaining({ delta: -500000 }),
    })
  )
})
it('a malformed server review never reaches MFA or allocation', async () => {
  const { prepare, mutations, verify } = setup()
  prepare.mockImplementationOnce(() => {
    throw new Error('Review unavailable')
  })
  await enterAdjustment('1')
  expect(
    await screen.findByRole('button', { name: 'Authorize transaction' })
  ).toBeDisabled()
  expect(mutations).not.toHaveBeenCalled()
  expect(verify).not.toHaveBeenCalled()
})

it('an active profile with ordinary role retains positive quota allocation eligibility', async () => {
  const { mutations, onClose } = setup({
    ...account,
    user_role: ROLE.USER,
    active_agent: true,
  })
  await enterAdjustment('1')
  await authorizeTransaction()
  await waitFor(() => expect(onClose).toHaveBeenCalledOnce())
  expect(mutations).toHaveBeenCalledWith(
    expect.objectContaining({
      data: expect.objectContaining({ agent_user_id: 7, delta: 500000 }),
    })
  )
})

it('cancelling MFA never sends an allocation or remembers an unsubmitted request', async () => {
  const { mutations, verify } = setup()
  await enterAdjustment('1')
  const review = await screen.findByRole('alertdialog', {
    name: 'Review transaction',
  })
  const authorize = within(review).getByRole('button', {
    name: 'Authorize transaction',
  })
  await waitFor(() => expect(authorize).toBeEnabled())
  await userEvent.click(authorize)
  const verification = await screen.findByRole('dialog', {
    name: 'Authorize this transaction',
  })
  await userEvent.click(
    within(verification).getByRole('button', { name: 'Cancel' })
  )
  await waitFor(() =>
    expect(
      screen.queryByRole('dialog', { name: 'Authorize this transaction' })
    ).not.toBeInTheDocument()
  )
  expect(verify).not.toHaveBeenCalled()
  expect(mutations).not.toHaveBeenCalled()
  expect(sessionStorage.getItem('agent-platform:pending:1')).toBeNull()
})

it('an expired MFA proof is rejected before the allocation request', async () => {
  const { mutations, verify } = setup()
  verify.mockResolvedValueOnce({
    data: {
      success: true,
      data: {
        proof_token: 'expired-test-proof',
        scope: 'agent.transaction',
        method: '2fa',
        expires_at: 1,
      },
    },
  })
  await enterAdjustment('1')
  await authorizeTransaction()
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Verification proof was not returned'
  )
  expect(mutations).not.toHaveBeenCalled()
  expect(sessionStorage.getItem('agent-platform:pending:1')).toBeNull()
})
