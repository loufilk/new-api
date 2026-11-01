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
import { Toaster, toast } from 'sonner'
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
function setup(children: React.ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <RedemptionsProvider>{children}</RedemptionsProvider>
      <Toaster />
    </QueryClientProvider>
  )
}
afterEach(() => {
  clients.forEach((client) => client.clear())
  clients.length = 0
  toast.dismiss()
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

it('single deletion requires confirmation and closes only after success', async () => {
  const remove = vi
    .spyOn(api, 'delete')
    .mockResolvedValue({ data: { success: true } })
  setup(<DeleteHarness />)
  const dialog = await screen.findByRole('alertdialog')
  expect(dialog).toHaveTextContent('customer code')
  expect(remove).not.toHaveBeenCalled()
  await userEvent.click(within(dialog).getByRole('button', { name: 'Delete' }))
  await waitFor(() =>
    expect(remove).toHaveBeenCalledExactlyOnceWith('/api/redemption/1')
  )
  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
})

it('failed deletion preserves the confirmation and reports the server reason', async () => {
  const remove = vi
    .spyOn(api, 'delete')
    .mockResolvedValue({ data: { success: false, message: 'Code not found' } })
  setup(<DeleteHarness />)
  await userEvent.click(
    within(await screen.findByRole('alertdialog')).getByRole('button', {
      name: 'Delete',
    })
  )
  expect(await screen.findByText('Code not found')).toBeVisible()
  expect(screen.getByRole('alertdialog')).toBeVisible()
  expect(remove).toHaveBeenCalledOnce()
})

it('delete-invalid warns about the cleanup batch and uses the ordinary endpoint', async () => {
  const remove = vi
    .spyOn(api, 'delete')
    .mockResolvedValue({ data: { success: true, data: 2 } })
  setup(<RedemptionsPrimaryButtons />)
  await userEvent.click(screen.getByRole('button', { name: 'Delete Invalid' }))
  const dialog = await screen.findByRole('alertdialog')
  expect(dialog).toHaveTextContent(
    'Each cleanup removes up to 1,000 used, disabled, or expired codes.'
  )
  expect(remove).not.toHaveBeenCalled()
  await userEvent.click(
    within(dialog).getByRole('button', { name: 'Delete Invalid' })
  )
  await waitFor(() =>
    expect(remove).toHaveBeenCalledExactlyOnceWith('/api/redemption/invalid')
  )
})

it.each([
  { status: 1, action: 'Disable', target: 2 },
  { status: 2, action: 'Enable', target: 1 },
])(
  'status $status submits only ID and the requested ordinary status',
  async ({ status, action, target }) => {
    const update = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true } })
    setup(
      <DataTableRowActions
        row={{ original: { ...code, status } } as Row<Redemption>}
      />
    )
    await userEvent.click(screen.getByRole('button', { name: 'Open menu' }))
    await userEvent.click(screen.getByRole('menuitem', { name: action }))
    await waitFor(() =>
      expect(update).toHaveBeenCalledExactlyOnceWith(
        '/api/redemption/?status_only=true',
        { id: 1, status: target }
      )
    )
  }
)
