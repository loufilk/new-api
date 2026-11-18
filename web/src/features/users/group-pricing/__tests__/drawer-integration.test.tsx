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
import { config, setupPricingRequests } from './fixtures'

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

it('saves group pricing independently and prevents Enter from submitting the surrounding account form', async () => {
  const { get, put } = setupPricingRequests()
  get.mockImplementation(async (url) => {
    if (url === '/api/group/') {
      return { data: { success: true, data: ['default', 'premium'] } }
    }
    if (url === '/api/authz/catalog') {
      return { data: { success: true, data: { resources: [], roles: [] } } }
    }
    if (url.endsWith('/group-pricing')) {
      return { data: { success: true, data: config } }
    }
    if (url.endsWith('/call-interception')) {
      return {
        data: {
          success: true,
          data: {
            user_id: 12,
            revision: 0,
            enabled: false,
            reply: '',
            updated_at: 0,
            updated_by: 0,
          },
        },
      }
    }
    return { data: { success: true, data: target } }
  })
  useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role: 100 })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const close = vi.fn()
  render(
    <QueryClientProvider client={client}>
      <UsersProvider>
        <UsersMutateDrawer open currentRow={target} onOpenChange={close} />
      </UsersProvider>
    </QueryClientProvider>
  )
  await screen.findByDisplayValue('Fixture user')
  const ratio = await screen.findByRole('spinbutton', {
    name: 'Personal multiplier',
  })
  fireEvent.change(screen.getByLabelText('Display Name'), {
    target: { value: 'Unsaved display name' },
  })
  fireEvent.change(ratio, { target: { value: '0.2' } })
  await userEvent.click(ratio)
  await userEvent.keyboard('{Enter}')
  expect(put).not.toHaveBeenCalled()
  await userEvent.click(
    screen.getByRole('button', { name: 'Save personal pricing' })
  )
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith('/api/user/12/group-pricing', {
      expected_revision: 1,
      ratios: { premium: 0.2 },
    })
  )
  expect(screen.getByLabelText('Display Name')).toHaveValue(
    'Unsaved display name'
  )
  expect(close).not.toHaveBeenCalled()
})
