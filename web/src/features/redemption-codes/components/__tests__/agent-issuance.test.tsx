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
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { RedemptionsMutateDrawer } from '../redemptions-mutate-drawer'
import { RedemptionsProvider } from '../redemptions-provider'
import { authorizeTransaction, transactionNetwork } from './transaction-fixture'

const clients: QueryClient[] = []
const availableQuota = 1500000

beforeEach(() => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 7, username: 'agent', role: ROLE.AGENT })
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
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        items: [
          {
            agent_user_id: 7,
            available_quota: availableQuota,
            reserved_quota: 0,
            spent_quota: 0,
            allocated_quota: availableQuota,
          },
        ],
        total: 1,
      },
    },
  })
})
afterEach(() => {
  clients.forEach((client) => client.clear())
  clients.length = 0
  useAuthStore.getState().auth.reset()
  sessionStorage.clear()
  vi.restoreAllMocks()
})

async function setup() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  clients.push(client)
  const network = transactionNetwork()
  const close = vi.fn()
  render(
    <QueryClientProvider client={client}>
      <RedemptionsProvider>
        <RedemptionsMutateDrawer open onOpenChange={close} />
      </RedemptionsProvider>
    </QueryClientProvider>
  )
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeEnabled()
  )
  fireEvent.change(screen.getByRole('textbox', { name: 'Name' }), {
    target: { value: 'customer' },
  })
  return { close, user: userEvent.setup(), ...network }
}

function enterBatch(amount: string, count: string) {
  fireEvent.change(screen.getByRole('spinbutton', { name: 'Quota (USD)' }), {
    target: { value: amount },
  })
  fireEvent.change(screen.getByRole('spinbutton', { name: 'Quantity' }), {
    target: { value: count },
  })
}

it('rejects a batch exceeding available quota without posting any codes', async () => {
  const post = vi.spyOn(api, 'post')
  await setup()
  enterBatch('1.0001', '3')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(
    await screen.findByText('Insufficient available agent quota')
  ).toBeInTheDocument()
  expect(post).not.toHaveBeenCalled()
})

it('rejects fractional batch quantities rather than truncating them', async () => {
  const post = vi.spyOn(api, 'post')
  await setup()
  enterBatch('1', '1.5')
  const form = document.querySelector('#redemption-form')
  if (!form) throw new Error('Expected redemption form')
  fireEvent.submit(form)
  await waitFor(() =>
    expect(
      screen.getByRole('spinbutton', { name: 'Quantity' })
    ).toHaveAttribute('aria-invalid', 'true')
  )
  expect(post).not.toHaveBeenCalled()
})

it.each([ROLE.AGENT, ROLE.USER])(
  'legacy or migrated agent role %s issues an exact-budget batch only after MFA and cannot duplicate submission',
  async (role) => {
    useAuthStore.getState().auth.setUser({
      id: 7,
      username: 'agent',
      role,
      permissions: { agent_history: true, agent_sales: true },
    })
    let finish!: (value: { data: unknown }) => void
    const { close, mutations, prepare } = await setup()
    mutations.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve
        })
    )
    enterBatch('1', '3')
    await userEvent.dblClick(
      screen.getByRole('button', { name: 'Save changes' })
    )
    expect(mutations).not.toHaveBeenCalled()
    await authorizeTransaction()
    await waitFor(() => expect(mutations).toHaveBeenCalledOnce())
    expect(
      screen.getByRole('button', { name: 'Authorize transaction' })
    ).toBeDisabled()
    await userEvent.keyboard('{Escape}')
    expect(close).not.toHaveBeenCalled()
    expect(prepare).toHaveBeenCalledWith(
      expect.objectContaining({
        action: 'redemption.create',
        payload: { name: 'customer', quota: 500000, count: 3, expired_time: 0 },
      })
    )
    expect(mutations).toHaveBeenCalledWith(
      expect.objectContaining({
        url: '/api/redemption/',
        data: {
          name: 'customer',
          quota: 500000,
          count: 3,
          expired_time: 0,
          request_id: prepare.mock.calls[0][0].request_id,
        },
        headers: { 'X-Security-Proof': 'test-single-use-proof' },
        skipAuthRefresh: true,
      })
    )
    finish({ data: { success: true, data: ['one', 'two', 'three'] } })
    await waitFor(() => expect(close).toHaveBeenCalledWith(false))
  }
)

it('an interrupted issuance cannot create a replacement batch before reconciliation', async () => {
  const { close, mutations } = await setup()
  mutations.mockRejectedValueOnce(new Error('Offline'))
  enterBatch('1', '1')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  await authorizeTransaction()
  expect(
    await screen.findByText('Do not submit a new request')
  ).toBeInTheDocument()
  expect(close).not.toHaveBeenCalled()
  expect(
    screen.getByRole('button', { name: 'Authorize transaction' })
  ).toBeDisabled()
  expect(mutations).toHaveBeenCalledOnce()
  expect(sessionStorage.getItem('agent-platform:pending:7')).not.toContain(
    'test-single-use-proof'
  )
})

it('closing an unsubmitted batch never creates redemption codes', async () => {
  const post = vi.spyOn(api, 'post')
  const { close, user } = await setup()
  await user.keyboard('{Escape}')
  expect(close).toHaveBeenCalledWith(false)
  expect(post).not.toHaveBeenCalled()
})
