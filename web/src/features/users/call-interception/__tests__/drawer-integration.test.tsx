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
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'

import { UsersMutateDrawer } from '../../components/users-mutate-drawer'
import { UsersProvider } from '../../components/users-provider'
import type { User } from '../../types'
import type { CallInterceptionUpdate } from '../types'
import { config, setupRequests } from './fixtures'

const target: User = {
  id: 12,
  username: 'fixture-user',
  display_name: 'Fixture user',
  role: 1,
  status: 1,
  quota: 0,
  used_quota: 0,
  request_count: 0,
  group: 'default',
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})

it('saves interception separately, does not submit other user fields, and discards stale drafts after close/reopen', async () => {
  const { get, put } = setupRequests()
  let saved = { ...config }
  get.mockImplementation(async (url) => {
    if (url === '/api/group/') {
      return { data: { success: true, data: ['default'] } }
    }
    if (url === '/api/authz/catalog') {
      return { data: { success: true, data: { resources: [], roles: [] } } }
    }
    if (url.endsWith('/call-interception')) {
      return { data: { success: true, data: saved } }
    }
    return { data: { success: true, data: target } }
  })
  put.mockImplementation(async (_url, payload) => {
    saved = {
      ...saved,
      ...(payload as CallInterceptionUpdate),
      revision: saved.revision + 1,
    }
    return { data: { success: true, data: saved } }
  })
  useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role: 100 })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const close = vi.fn()
  const view = (open: boolean, row = target) => (
    <QueryClientProvider client={client}>
      <UsersProvider>
        <UsersMutateDrawer open={open} onOpenChange={close} currentRow={row} />
      </UsersProvider>
    </QueryClientProvider>
  )
  const rendered = render(view(true))
  await screen.findByDisplayValue('Fixture user')
  const reply = await screen.findByRole('textbox', { name: 'Custom reply' })
  fireEvent.change(screen.getByLabelText('Display Name'), {
    target: { value: 'Not yet saved' },
  })
  fireEvent.change(reply, { target: { value: 'Saved interception reply' } })
  await userEvent.click(
    screen.getByRole('button', { name: 'Save interception settings' })
  )
  await userEvent.click(screen.getByRole('button', { name: 'Apply settings' }))
  await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
  expect(put.mock.calls[0][0]).toBe('/api/user/12/call-interception')
  expect(put.mock.calls[0][1]).not.toHaveProperty('display_name')
  expect(close).not.toHaveBeenCalled()
  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
  fireEvent.change(reply, { target: { value: 'Discard me on close' } })
  await userEvent.click(screen.getAllByRole('button', { name: 'Close' })[0])
  expect(close).toHaveBeenCalledWith(false)
  rendered.rerender(view(false))
  rendered.rerender(view(true))
  await waitFor(() =>
    expect(screen.getByRole('textbox', { name: 'Custom reply' })).toHaveValue(
      'Saved interception reply'
    )
  )
})
