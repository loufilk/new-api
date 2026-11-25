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
import { act, cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { GroupMarketActivityPreview } from '../group-market-activity-preview'

let client: QueryClient
function open(role = 100) {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'admin', role })
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <GroupMarketActivityPreview />
    </QueryClientProvider>
  )
}
afterEach(() => {
  cleanup()
  client.clear()
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
})

it('renders real group additions and rate deltas newest first without a publishing or billing write', async () => {
  const recent = 1780000000
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        groups: {
          regular: {
            rate_changes: [
              { old_ratio: 0.2, new_ratio: 0, effective_at: recent },
              { old_ratio: 0, new_ratio: 0.2, effective_at: recent - 1 },
            ],
          },
          addition: { first_configured_at: recent + 1, is_new: true },
          older: { first_configured_at: recent - 5000 },
          untouched: {},
        },
      },
    },
  })
  const post = vi.spyOn(api, 'post'),
    put = vi.spyOn(api, 'put'),
    patch = vi.spyOn(api, 'patch')
  open()
  const table = within(await screen.findByRole('table'))
  const rows = table.getAllByRole('row').slice(1)
  expect(rows).toHaveLength(3)
  expect(rows[0]).toHaveTextContent('addition')
  expect(rows[0]).toHaveTextContent('Group added')
  expect(rows[1]).toHaveTextContent('−0.2×')
  expect(rows[2]).toHaveTextContent('+0.2×')
  expect(rows[1].querySelector('time')).toHaveAttribute(
    'datetime',
    new Date(recent * 1000).toISOString()
  )
  expect(screen.queryByText('untouched')).not.toBeInTheDocument()
  expect(screen.queryByText('older')).not.toBeInTheDocument()
  expect(screen.queryByRole('button')).not.toBeInTheDocument()
  expect(get).toHaveBeenCalledWith(
    '/api/option/group-catalog/activity',
    expect.objectContaining({ disableDuplicate: true })
  )
  expect(post).not.toHaveBeenCalled()
  expect(put).not.toHaveBeenCalled()
  expect(patch).not.toHaveBeenCalled()
})

it('distinguishes an unavailable snapshot from an empty history and can retry without writing', async () => {
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValueOnce({ data: { success: true, data: {} } })
    .mockResolvedValueOnce({ data: { success: true, data: { groups: {} } } })
  open()
  const retry = await screen.findByRole('button', { name: 'Retry' })
  expect(screen.queryByText('No recent group activity')).not.toBeInTheDocument()
  await userEvent.click(retry)
  expect(await screen.findByText('No recent group activity')).toBeVisible()
  expect(get).toHaveBeenCalledTimes(2)
})

it('renders no privileged history for non-root accounts, including after changing accounts', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        groups: {
          internal: { first_configured_at: 1780000000, is_new: true },
        },
      },
    },
  })
  open(10)
  expect(get).not.toHaveBeenCalled()
  act(() =>
    useAuthStore.getState().auth.setUser({ id: 2, username: 'root', role: 100 })
  )
  expect(await screen.findByText('internal')).toBeVisible()
  act(() =>
    useAuthStore.getState().auth.setUser({ id: 1, username: 'admin', role: 10 })
  )
  expect(screen.queryByText('internal')).not.toBeInTheDocument()
  expect(get).toHaveBeenCalledTimes(1)
})

it('keeps the preview compact while allowing all returned activity to be inspected', async () => {
  const changes = Array.from({ length: 10 }, (_, i) => ({
    old_ratio: i,
    new_ratio: i + 1,
    effective_at: 1780000000 + i,
  }))
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: { groups: { many: { rate_changes: changes } } },
    },
  })
  open()
  const more = await screen.findByRole('button', { name: 'View all' })
  const table = within(screen.getByRole('table'))
  expect(table.getAllByRole('row')).toHaveLength(9)
  await userEvent.click(more)
  expect(table.getAllByRole('row')).toHaveLength(11)
  await userEvent.click(screen.getByRole('button', { name: 'Collapse' }))
  expect(table.getAllByRole('row')).toHaveLength(9)
})
