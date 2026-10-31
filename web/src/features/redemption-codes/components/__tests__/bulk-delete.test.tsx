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
import {
  QueryClient,
  QueryClientProvider,
  useQuery,
} from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Toaster, toast } from 'sonner'
import { afterEach, expect, test, vi } from 'vitest'

import { useDataTable } from '@/components/data-table'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { getRedemptions } from '../../api'
import type { Redemption } from '../../types'
import { DataTableBulkActions } from '../data-table-bulk-actions'
import { RedemptionsProvider, useRedemptions } from '../redemptions-provider'
import { authorizeTransaction, transactionNetwork } from './transaction-fixture'

const codes: Redemption[] = [11, 22, 33].map((id) => ({
  id,
  user_id: 1,
  name: `code-${id}`,
  key: `key-${id}`,
  status: 1,
  quota: 100,
  created_time: 1,
  redeemed_time: 0,
  expired_time: 0,
  used_user_id: 0,
}))
const columns = [{ accessorKey: 'name' }]
const clients: QueryClient[] = []

function RedemptionList() {
  const { refreshTrigger } = useRedemptions()
  const { data } = useQuery({
    queryKey: ['redemptions', refreshTrigger],
    queryFn: () => getRedemptions(),
  })
  const { table } = useDataTable({
    data: data?.data?.items ?? [],
    columns,
    enableRowSelection: true,
    getRowId: (row) => String(row.id),
  })
  return (
    <>
      {table.getRowModel().rows.map((row) => (
        <label key={row.id}>
          <input
            type='checkbox'
            checked={row.getIsSelected()}
            onChange={row.getToggleSelectedHandler()}
          />
          {row.original.name}
        </label>
      ))}
      <DataTableBulkActions table={table} />
    </>
  )
}

async function setup(protectedCode = false) {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'operator', role: ROLE.ADMIN })
  let remaining = codes.map((code) => ({
    ...code,
    source_protected: protectedCode && code.id === 11,
  }))
  vi.spyOn(api, 'get').mockImplementation(async () => ({
    data: {
      success: true,
      data: { items: remaining, total: remaining.length },
    },
  }))
  const network = transactionNetwork()
  const remove = network.mutations.mockImplementation(async (config) => {
    const { ids } = config.data as { ids: number[] }
    const count = remaining.filter((code) => ids.includes(code.id)).length
    remaining = remaining.filter((code) => !ids.includes(code.id))
    return { data: { success: true, data: count } }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <RedemptionsProvider>
        <RedemptionList />
      </RedemptionsProvider>
      <Toaster />
    </QueryClientProvider>
  )
  await screen.findByRole('checkbox', { name: 'code-11' })
  return { user: userEvent.setup(), remove, prepare: network.prepare }
}

afterEach(() => {
  toast.dismiss()
  for (const client of clients) client.clear()
  clients.length = 0
  useAuthStore.getState().auth.reset()
  sessionStorage.clear()
  vi.restoreAllMocks()
})

test('shows deletion only for selected codes and cancellation sends no requests', async () => {
  const { user, remove } = await setup()
  expect(
    screen.queryByRole('button', { name: 'Delete selected redemption codes' })
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('checkbox', { name: 'code-11' }))
  const button = screen.getByRole('button', {
    name: 'Delete selected redemption codes',
  })
  button.focus()
  await user.keyboard('{Enter}')
  const dialog = await screen.findByRole('alertdialog')
  expect(dialog).toHaveAccessibleName('Delete 1 redemption codes?')
  await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
  expect(remove).not.toHaveBeenCalled()
  expect(screen.getByRole('checkbox', { name: 'code-11' })).toBeChecked()
})

test('confirmation deletes only selected codes, disables repeat submissions and refreshes the list', async () => {
  const { user, remove } = await setup()
  let release!: () => void
  const pending = new Promise<void>((resolve) => {
    release = resolve
  })
  const originalDelete = remove.getMockImplementation()
  if (!originalDelete) throw new Error('Missing batch delete mock')
  remove.mockImplementationOnce(async (config) => {
    await pending
    return originalDelete(config)
  })
  await user.click(screen.getByRole('checkbox', { name: 'code-11' }))
  await user.click(screen.getByRole('checkbox', { name: 'code-22' }))
  await user.click(
    screen.getByRole('button', { name: 'Delete selected redemption codes' })
  )
  const dialog = await screen.findByRole('alertdialog')
  expect(dialog).toHaveAccessibleName('Delete 2 redemption codes?')
  expect(remove).not.toHaveBeenCalled()
  await user.click(within(dialog).getByRole('button', { name: 'Delete' }))
  await authorizeTransaction()
  const review = screen.getByRole('alertdialog', { name: 'Review transaction' })
  expect(
    within(review).getByRole('button', { name: 'Authorize transaction' })
  ).toBeDisabled()
  expect(within(review).getByRole('button', { name: 'Cancel' })).toBeDisabled()
  release()
  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
  await waitFor(() =>
    expect(
      screen.queryByRole('checkbox', { name: 'code-11' })
    ).not.toBeInTheDocument()
  )
  expect(remove).toHaveBeenCalledOnce()
  expect(remove).toHaveBeenCalledWith(
    expect.objectContaining({
      url: '/api/redemption/batch',
      method: 'post',
      data: { ids: [11, 22], request_id: expect.any(String) },
      headers: { 'X-Security-Proof': 'test-single-use-proof' },
      skipAuthRefresh: true,
    })
  )
  expect(screen.getByRole('checkbox', { name: 'code-33' })).not.toBeChecked()
  expect(screen.queryByRole('toolbar')).not.toBeInTheDocument()
  expect(document.body).toHaveTextContent(
    'Successfully deleted 2 redemption codes'
  )
})

test.each(['server', 'network'])(
  '%s failure preserves selected codes and requires reconciliation instead of replay',
  async (failure) => {
    const { user, remove } = await setup()
    if (failure === 'server') {
      remove.mockResolvedValueOnce({ data: { success: false } })
    } else remove.mockRejectedValueOnce(new Error('Network unavailable'))
    await user.click(screen.getByRole('checkbox', { name: 'code-11' }))
    await user.click(screen.getByRole('checkbox', { name: 'code-22' }))
    await user.click(
      screen.getByRole('button', { name: 'Delete selected redemption codes' })
    )
    await user.click(
      within(await screen.findByRole('alertdialog')).getByRole('button', {
        name: 'Delete',
      })
    )
    await authorizeTransaction()
    expect(
      await screen.findByText('Do not submit a new request')
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Authorize transaction' })
    ).toBeDisabled()
    expect(screen.getByLabelText('code-11')).toBeChecked()
    expect(screen.getByLabelText('code-22')).toBeChecked()
    expect(remove).toHaveBeenCalledOnce()
  }
)

test('a selection containing protected rights cannot enter batch deletion', async () => {
  const { user, remove, prepare } = await setup(true)
  await user.click(screen.getByRole('checkbox', { name: 'code-11' }))
  await user.click(screen.getByRole('checkbox', { name: 'code-22' }))
  expect(
    screen.getByRole('button', { name: 'Delete selected redemption codes' })
  ).toBeDisabled()
  expect(prepare).not.toHaveBeenCalled()
  expect(remove).not.toHaveBeenCalled()
})
