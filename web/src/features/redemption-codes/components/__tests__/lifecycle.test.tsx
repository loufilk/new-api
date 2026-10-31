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
import type { Row } from '@tanstack/react-table'
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useEffect } from 'react'
import { afterEach, expect, it, vi } from 'vitest'

import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import type { Redemption } from '../../types'
import { DataTableRowActions } from '../data-table-row-actions'
import { RedemptionsDeleteDialog } from '../redemptions-delete-dialog'
import { RedemptionsPrimaryButtons } from '../redemptions-primary-buttons'
import { RedemptionsProvider, useRedemptions } from '../redemptions-provider'
import { authorizeTransaction, transactionNetwork } from './transaction-fixture'

const code: Redemption = {
  id: 1,
  user_id: 7,
  budget_agent_id: 7,
  version: 3,
  budget_reserved: true,
  source_protected: false,
  delivery_id: 0,
  customer_user_id: 0,
  name: 'customer code',
  key: 'test-code',
  status: 1,
  quota: 500000,
  created_time: 1,
  expired_time: 0,
  redeemed_time: 0,
  used_user_id: 0,
}
const clients: QueryClient[] = []
function DeleteHarness() {
  const redemptions = useRedemptions()
  useEffect(() => {
    redemptions.setCurrentRow(code)
    redemptions.setOpen('delete')
    // Seed the provider once, not after its own state updates.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  return <RedemptionsDeleteDialog />
}
function setup(children: React.ReactNode, role: number = ROLE.ADMIN, id = 1) {
  useAuthStore.getState().auth.setUser({ id, username: 'operator', role })
  const network = transactionNetwork()
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <RedemptionsProvider>{children}</RedemptionsProvider>
    </QueryClientProvider>
  )
  return network
}
afterEach(() => {
  clients.forEach((client) => client.clear())
  clients.length = 0
  useAuthStore.getState().auth.reset()
  sessionStorage.clear()
  vi.restoreAllMocks()
})

it('used redemption codes expose no edit or re-enable control', async () => {
  setup(
    <DataTableRowActions
      row={{ original: { ...code, status: 3 } } as Row<Redemption>}
    />
  )
  expect(screen.getByRole('button', { name: 'Edit' })).toBeDisabled()
  await userEvent.click(screen.getByRole('button', { name: 'Open menu' }))
  expect(
    screen.queryByRole('menuitem', { name: 'Enable' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('menuitem', { name: 'Disable' })
  ).not.toBeInTheDocument()
})
it('single deletion is parameter-bound and interrupted results cannot be blindly retried', async () => {
  const { prepare, mutations } = setup(<DeleteHarness />)
  mutations.mockRejectedValueOnce(new Error('Offline'))
  await userEvent.click(
    within(await screen.findByRole('alertdialog')).getByRole('button', {
      name: 'Delete',
    })
  )
  expect(mutations).not.toHaveBeenCalled()
  await authorizeTransaction()
  expect(
    await screen.findByText('Do not submit a new request')
  ).toBeInTheDocument()
  expect(
    screen.getByRole('button', { name: 'Authorize transaction' })
  ).toBeDisabled()
  expect(prepare).toHaveBeenCalledWith(
    expect.objectContaining({
      action: 'redemption.admin.delete',
      payload: { ids: [1], invalid_only: false, agent_user_id: 0 },
    })
  )
  expect(mutations).toHaveBeenCalledWith(
    expect.objectContaining({
      url: '/api/redemption/1/',
      method: 'delete',
      params: { request_id: prepare.mock.calls[0][0].request_id },
      headers: { 'X-Security-Proof': 'test-single-use-proof' },
      skipAuthRefresh: true,
    })
  )
  expect(mutations).toHaveBeenCalledOnce()
})
it('delete-invalid preserves the visible agent scope through review and final query', async () => {
  const { mutations, prepare } = setup(
    <RedemptionsPrimaryButtons agentId={7} />
  )
  await userEvent.click(screen.getByRole('button', { name: 'Delete Invalid' }))
  const dialog = await screen.findByRole('alertdialog')
  expect(dialog).toHaveTextContent(
    'Only invalid codes belonging to agent #7 will be deleted.'
  )
  expect(dialog).toHaveTextContent(
    'Each cleanup removes up to 1,000 used, disabled, or expired codes. Repeat cleanup if invalid codes remain.'
  )
  await userEvent.click(
    within(dialog).getByRole('button', { name: 'Delete Invalid' })
  )
  await authorizeTransaction()
  await waitFor(() => expect(mutations).toHaveBeenCalledOnce())
  expect(prepare).toHaveBeenCalledWith(
    expect.objectContaining({
      payload: { ids: null, invalid_only: true, agent_user_id: 7 },
    })
  )
  expect(mutations).toHaveBeenCalledWith(
    expect.objectContaining({
      url: '/api/redemption/invalid',
      method: 'delete',
      params: {
        agent_user_id: 7,
        request_id: prepare.mock.calls[0][0].request_id,
      },
    })
  )
})
it.each([ROLE.AGENT, ROLE.ADMIN])(
  'status changes use the role-matched prepare action and retain the observed version for role %s',
  async (role) => {
    const { mutations, prepare } = setup(
      <DataTableRowActions row={{ original: code } as Row<Redemption>} />,
      role,
      7
    )
    await userEvent.click(screen.getByRole('button', { name: 'Open menu' }))
    await userEvent.click(screen.getByRole('menuitem', { name: 'Disable' }))
    await authorizeTransaction()
    await waitFor(() => expect(mutations).toHaveBeenCalledOnce())
    expect(prepare).toHaveBeenCalledWith(
      expect.objectContaining({
        action:
          role === ROLE.AGENT ? 'redemption.update' : 'redemption.admin.update',
        payload: {
          id: 1,
          version: 3,
          name: '',
          status: 2,
          quota: 0,
          expired_time: 0,
          status_only: true,
        },
      })
    )
    expect(mutations).toHaveBeenCalledWith(
      expect.objectContaining({
        url: '/api/redemption/',
        method: 'put',
        params: { status_only: true },
        data: {
          id: 1,
          version: 3,
          name: '',
          status: 2,
          quota: 0,
          expired_time: 0,
          request_id: prepare.mock.calls[0][0].request_id,
        },
      })
    )
  }
)
it.each([
  { source_protected: true },
  { customer_user_id: 99 },
  { delivery_id: 8 },
])(
  'protected rights $source_protected $customer_user_id $delivery_id prevent legacy mutations',
  async (flags) => {
    setup(
      <DataTableRowActions
        row={{ original: { ...code, ...flags } } as Row<Redemption>}
      />
    )
    expect(screen.getByRole('button', { name: 'Edit' })).toBeDisabled()
    await userEvent.click(screen.getByRole('button', { name: 'Open menu' }))
    expect(
      screen.queryByRole('menuitem', { name: 'Disable' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('menuitem', { name: 'Delete' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('menuitem', { name: 'Review cancellation' })
    ).not.toBeInTheDocument()
  }
)
it('the owner may cancel an unbound paid-source reservation only with obligation evidence and MFA', async () => {
  const { prepare, mutations } = setup(
    <DataTableRowActions
      row={{ original: { ...code, source_protected: true } } as Row<Redemption>}
    />,
    ROLE.AGENT,
    7
  )
  await userEvent.click(screen.getByRole('button', { name: 'Open menu' }))
  await userEvent.click(
    screen.getByRole('menuitem', { name: 'Review cancellation' })
  )
  expect(
    await screen.findByRole('dialog', { name: 'Review cancellation' })
  ).toHaveTextContent(
    'An unclaimed delivery may already have been sold offline.'
  )
  fireEvent.change(
    screen.getByRole('textbox', {
      name: 'Customer obligation evidence reference',
    }),
    { target: { value: 'case-42 resolved with customer' } }
  )
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
  expect(prepare).not.toHaveBeenCalled()
  expect(mutations).not.toHaveBeenCalled()
  await userEvent.click(
    screen.getByRole('checkbox', {
      name: 'All customer obligations have been verified as resolved',
    })
  )
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
  await authorizeTransaction()
  await waitFor(() => expect(mutations).toHaveBeenCalledOnce())
  expect(prepare).toHaveBeenCalledWith(
    expect.objectContaining({
      action: 'delivery.cancel',
      payload: {
        redemption_id: 1,
        version: 3,
        customer_obligations_resolved: true,
        evidence_reference: 'case-42 resolved with customer',
      },
    })
  )
  expect(mutations).toHaveBeenCalledWith(
    expect.objectContaining({
      url: '/api/agent-platform/deliveries/cancel-code',
      headers: { 'X-Security-Proof': 'test-single-use-proof' },
      skipAuthRefresh: true,
    })
  )
})
