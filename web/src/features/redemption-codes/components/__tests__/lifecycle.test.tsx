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
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useEffect } from 'react'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import type { Redemption } from '../../types'
import { DataTableRowActions } from '../data-table-row-actions'
import { RedemptionsDeleteDialog } from '../redemptions-delete-dialog'
import { RedemptionsPrimaryButtons } from '../redemptions-primary-buttons'
import { RedemptionsProvider, useRedemptions } from '../redemptions-provider'

const code: Redemption = {
  id: 1,
  user_id: 7,
  budget_agent_id: 7,
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
    // Seed the real provider once for this deletion workflow.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  return <RedemptionsDeleteDialog />
}
function setup(children: React.ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <RedemptionsProvider>{children}</RedemptionsProvider>
    </QueryClientProvider>
  )
}
afterEach(() => {
  clients.forEach((client) => client.clear())
  clients.length = 0
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

it('single deletion failure keeps its confirmation open for a safe retry', async () => {
  const remove = vi
    .spyOn(api, 'delete')
    .mockRejectedValueOnce(new Error('Offline'))
    .mockResolvedValueOnce({ data: { success: true } })
  setup(<DeleteHarness />)
  await userEvent.click(
    within(await screen.findByRole('alertdialog')).getByRole('button', {
      name: 'Delete',
    })
  )
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Delete' })).toBeEnabled()
  )
  expect(screen.getByRole('alertdialog')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Delete' }))
  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
  expect(remove).toHaveBeenCalledTimes(2)
})

it('delete-invalid transport failures keep the dialog open and re-enable retry', async () => {
  const remove = vi.spyOn(api, 'delete').mockRejectedValue(new Error('Offline'))
  setup(<RedemptionsPrimaryButtons />)
  await userEvent.click(screen.getByRole('button', { name: 'Delete Invalid' }))
  const dialog = await screen.findByRole('alertdialog')
  await userEvent.click(
    within(dialog).getByRole('button', { name: 'Delete Invalid' })
  )
  await waitFor(() => expect(remove).toHaveBeenCalledTimes(1))
  expect(dialog).toBeInTheDocument()
  expect(
    within(dialog).getByRole('button', { name: 'Delete Invalid' })
  ).toBeEnabled()
})

it('delete-invalid forwards the visible agent scope rather than clearing all agents', async () => {
  const remove = vi
    .spyOn(api, 'delete')
    .mockResolvedValue({ data: { success: true, data: 1 } })
  setup(<RedemptionsPrimaryButtons agentId={7} />)
  await userEvent.click(screen.getByRole('button', { name: 'Delete Invalid' }))
  const dialog = await screen.findByRole('alertdialog')
  expect(dialog).toHaveTextContent(
    'Only invalid codes belonging to agent #7 will be deleted.'
  )
  await userEvent.click(
    within(dialog).getByRole('button', { name: 'Delete Invalid' })
  )
  await waitFor(() =>
    expect(remove).toHaveBeenCalledWith(
      '/api/redemption/invalid?agent_user_id=7'
    )
  )
})

it('delete-invalid explains the bounded cleanup and how to continue before confirmation', async () => {
  setup(<RedemptionsPrimaryButtons />)
  await userEvent.click(screen.getByRole('button', { name: 'Delete Invalid' }))
  expect(await screen.findByRole('alertdialog')).toHaveTextContent(
    'Each cleanup removes up to 1,000 used, disabled, or expired codes. Repeat cleanup if invalid codes remain.'
  )
})
