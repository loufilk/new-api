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
import { afterEach, expect, test, vi } from 'vitest'

import { SupportCenter } from '@/features/support'
import { api, type ApiRequestConfig } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

afterEach(() => useAuthStore.getState().auth.reset())

test('resetting filters restores search, status, unread state and the unfiltered ticket list', async () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 299, username: 'admin', role: 100 })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  const ticket = {
    id: 72,
    user_id: 201,
    subject: 'API support request',
    status: 'processing',
    user_unread: false,
    admin_unread: true,
    last_message_at: 1,
    created_at: 1,
    updated_at: 1,
  }
  vi.spyOn(api, 'get').mockImplementation(
    async (url, config: ApiRequestConfig = {}) => {
      if (url !== '/api/admin/support/tickets') {
        throw new Error('Unexpected request')
      }
      const filtered =
        config.params?.keyword || config.params?.status || config.params?.unread
      return {
        data: {
          success: true,
          message: '',
          data: {
            items: filtered ? [] : [ticket],
            total: filtered ? 0 : 1,
            page: 1,
            page_size: 20,
            status_counts: { processing: 1, awaiting_user: 0, resolved: 0 },
          },
        },
      }
    }
  )
  const rendered = render(
    <QueryClientProvider client={client}>
      <SupportCenter />
    </QueryClientProvider>
  )
  try {
    const user = userEvent.setup()
    await screen.findByRole('button', { name: /API support request/ })
    await user.type(
      screen.getByRole('textbox', { name: 'Search tickets' }),
      'missing'
    )
    await user.click(screen.getByRole('button', { name: 'Search' }))
    await user.click(screen.getByRole('combobox', { name: 'Status' }))
    await user.click(screen.getByRole('option', { name: 'Resolved' }))
    await user.click(screen.getByRole('switch', { name: 'Unread only' }))
    await waitFor(() =>
      expect(
        screen.queryByRole('button', { name: /API support request/ })
      ).not.toBeInTheDocument()
    )
    await user.click(screen.getByRole('button', { name: 'Reset filters' }))
    await screen.findByRole('button', { name: /API support request/ })
    expect(screen.getByRole('textbox', { name: 'Search tickets' })).toHaveValue(
      ''
    )
    expect(screen.getByRole('combobox', { name: 'Status' })).toHaveTextContent(
      'All statuses'
    )
    expect(
      screen.getByRole('switch', { name: 'Unread only' })
    ).not.toBeChecked()
    expect(
      screen.queryByRole('button', { name: 'Reset filters' })
    ).not.toBeInTheDocument()
  } finally {
    rendered.unmount()
    client.clear()
  }
})

test('a ticket query failure shows unavailable counts instead of zero', async () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 299, username: 'admin', role: 100 })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  vi.spyOn(api, 'get').mockRejectedValue(
    new Error('Support service unavailable')
  )
  const rendered = render(
    <QueryClientProvider client={client}>
      <SupportCenter />
    </QueryClientProvider>
  )
  try {
    await screen.findByRole('button', { name: 'Retry' })
    expect(screen.getAllByText('—')).toHaveLength(3)
    expect(screen.queryByText('0', { exact: true })).not.toBeInTheDocument()
  } finally {
    rendered.unmount()
    client.clear()
  }
})
