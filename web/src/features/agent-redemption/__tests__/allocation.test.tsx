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
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { AllocationDialog } from '../components/allocation-dialog'
import type { AgentAccount } from '../types'

const account: AgentAccount = {
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
  return { onClose, user: userEvent.setup() }
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
  for (const client of clients) client.clear()
  clients.length = 0
  vi.restoreAllMocks()
})

describe('agent quota allocation', () => {
  it('prevents reclaiming reserved quota even when the total budget is higher', async () => {
    const post = vi.spyOn(api, 'post')
    setup()
    await enterAdjustment('-2')
    expect(
      await screen.findByText(
        'Adjustment exceeds the available quota or safe integer range'
      )
    ).toBeInTheDocument()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(post).not.toHaveBeenCalled()
  })
  it('cancelling confirmation sends no quota adjustment', async () => {
    const post = vi.spyOn(api, 'post')
    const { user } = setup()
    await enterAdjustment('1')
    const confirm = await screen.findByRole('alertdialog')
    await user.click(within(confirm).getByRole('button', { name: 'Cancel' }))
    expect(post).not.toHaveBeenCalled()
    expect(
      screen.getByRole('spinbutton', { name: 'Adjustment (USD)' })
    ).toHaveValue(1)
  })
  it('requires a reason and rejects a zero quota adjustment', async () => {
    const post = vi.spyOn(api, 'post')
    setup()
    await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
    expect(
      await screen.findByText('Enter a non-zero quota adjustment')
    ).toBeInTheDocument()
    expect(screen.getByText('Reason is required')).toBeInTheDocument()
    expect(post).not.toHaveBeenCalled()
  })
  it('blocks duplicate confirmations and closing until an allocation resolves', async () => {
    let resolve!: (value: unknown) => void
    const post = vi.spyOn(api, 'post').mockImplementation(
      () =>
        new Promise((done) => {
          resolve = done
        })
    )
    const { user, onClose } = setup()
    await enterAdjustment('1')
    const dialog = await screen.findByRole('alertdialog')
    const confirm = within(dialog).getByRole('button', { name: 'Confirm' })
    await user.dblClick(confirm)
    expect(confirm).toBeDisabled()
    expect(
      within(dialog).getByRole('button', { name: 'Cancel' })
    ).toBeDisabled()
    await user.keyboard('{Escape}')
    expect(onClose).not.toHaveBeenCalled()
    expect(post).toHaveBeenCalledTimes(1)
    expect(post).toHaveBeenCalledWith(
      '/api/agent-redemption/allocations',
      expect.objectContaining({
        agent_user_id: 7,
        delta: 500000,
        reason: 'Monthly allocation',
        request_id: expect.any(String),
      })
    )
    resolve({ data: { success: true, data: { account, replayed: false } } })
    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1))
  })
  it('keeps a failed allocation open and reuses its request ID when retried', async () => {
    const post = vi
      .spyOn(api, 'post')
      .mockRejectedValueOnce(new Error('Offline'))
      .mockResolvedValueOnce({
        data: { success: true, data: { account, replayed: true } },
      })
    const { onClose } = setup()
    await enterAdjustment('1')
    await userEvent.click(
      within(await screen.findByRole('alertdialog')).getByRole('button', {
        name: 'Confirm',
      })
    )
    await waitFor(() =>
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    )
    expect(onClose).not.toHaveBeenCalled()
    await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
    await userEvent.click(
      within(await screen.findByRole('alertdialog')).getByRole('button', {
        name: 'Confirm',
      })
    )
    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1))
    expect(post.mock.calls[0]?.[1]).toEqual(post.mock.calls[1]?.[1])
  })
})

it('does not add new quota to a disabled or downgraded account', async () => {
  const post = vi.spyOn(api, 'post')
  setup({ ...account, user_role: 1, user_status: 2 })
  await enterAdjustment('1')
  expect(
    await screen.findByText(
      'Agent account must be active before issuing redemption codes'
    )
  ).toBeInTheDocument()
  expect(post).not.toHaveBeenCalled()
})

it('allows reclaiming a historical account available balance after role removal', async () => {
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: { account, replayed: false } },
  })
  const { onClose } = setup({ ...account, user_role: 1, user_deleted: true })
  await enterAdjustment('-1')
  await userEvent.click(
    within(await screen.findByRole('alertdialog')).getByRole('button', {
      name: 'Confirm',
    })
  )
  await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1))
  expect(post).toHaveBeenCalledWith(
    '/api/agent-redemption/allocations',
    expect.objectContaining({ delta: -500000 })
  )
})
