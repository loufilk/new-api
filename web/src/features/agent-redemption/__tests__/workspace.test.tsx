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
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { AgentRedemption } from '..'

const clients: QueryClient[] = []
function setup(role: number) {
  useAuthStore.getState().auth.setUser({ id: 7, username: 'agent', role })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <AgentRedemption />
    </QueryClientProvider>
  )
}
afterEach(() => {
  clients.forEach((client) => client.clear())
  clients.length = 0
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
})

it('shows an agent only their budget, ledger and invitation link with no administrative controls', async () => {
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/agent-redemption/accounts') {
      return {
        data: {
          success: true,
          data: {
            items: [
              {
                agent_user_id: 7,
                available_quota: 1,
                reserved_quota: 2,
                spent_quota: 3,
                allocated_quota: 6,
              },
            ],
            total: 1,
          },
        },
      }
    }
    if (url === '/api/user/aff') {
      return { data: { success: true, data: 'agent-code' } }
    }
    return { data: { success: true, data: { items: [], total: 0 } } }
  })
  setup(ROLE.AGENT)
  expect(await screen.findByLabelText('Agent quota budget')).toBeInTheDocument()
  expect(
    screen.queryByRole('tab', { name: 'Invitation policy' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('tab', { name: 'Agent accounts' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Adjust quota' })
  ).not.toBeInTheDocument()
  expect(screen.queryByLabelText('Agent ID')).not.toBeInTheDocument()
  expect(get).toHaveBeenCalledWith(
    '/api/agent-redemption/accounts/7/ledger',
    expect.anything()
  )
  await userEvent.click(screen.getByRole('tab', { name: 'Invitations' }))
  expect(
    await screen.findByDisplayValue(
      `${window.location.origin}/sign-up?aff=agent-code`
    )
  ).toHaveAttribute('readonly')
  expect(
    screen.getByRole('button', { name: 'Copy invitation link' })
  ).toBeEnabled()
  expect(
    get.mock.calls.some(
      ([url]) => url === '/api/agent-redemption/invitation-policy'
    )
  ).toBe(false)
})

it('distinguishes a ledger request failure from an empty ledger and offers retry', async () => {
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (String(url).endsWith('/ledger')) throw new Error('Offline')
    return { data: { success: true, data: { items: [], total: 0 } } }
  })
  setup(ROLE.AGENT)
  expect(await screen.findByRole('button', { name: 'Retry' })).toBeEnabled()
  expect(screen.queryByText('No quota ledger entries')).not.toBeInTheDocument()
  get.mockImplementation(async () => ({
    data: { success: true, data: { items: [], total: 0 } },
  }))
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  await waitFor(() =>
    expect(screen.getByText('No quota ledger entries')).toBeInTheDocument()
  )
})
