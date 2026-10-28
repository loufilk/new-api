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

afterEach(() => {
  useAuthStore.getState().auth.reset()
})

function createClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, refetchOnWindowFocus: false },
    },
  })
}

test('the selected conversation stays visible after reading the last unread ticket empties the unread list', async () => {
  const client = createClient()
  useAuthStore.getState().auth.setUser({ id: 201, username: 'user', role: 1 })

  const unreadTicket = {
    id: 7,
    user_id: 201,
    subject: 'Audit unread support ticket',
    status: 'awaiting_user',
    user_unread: true,
    admin_unread: false,
    last_message_at: 1,
    created_at: 1,
    updated_at: 1,
  }
  const readTicket = {
    ...unreadTicket,
    id: 8,
    subject: 'Previously read ticket',
    user_unread: false,
  }

  vi.spyOn(api, 'get').mockImplementation(
    async (url, config: ApiRequestConfig = {}) => {
      if (url === '/api/support/tickets') {
        const items = [unreadTicket, readTicket].filter(
          (item) => !config?.params?.unread || item.user_unread
        )
        return {
          data: {
            success: true,
            message: '',
            data: {
              items: structuredClone(items),
              total: items.length,
              page: 1,
              page_size: 20,
              status_counts: {
                processing: 0,
                awaiting_user: items.length,
                resolved: 0,
              },
            },
          },
        }
      }
      if (url === '/api/support/tickets/7') {
        return {
          data: {
            success: true,
            message: '',
            data: {
              ticket: structuredClone(unreadTicket),
              messages: [
                {
                  id: 1,
                  ticket_id: 7,
                  author_role: 'admin',
                  content: 'The support answer must stay readable',
                  created_at: 1,
                },
              ],
            },
          },
        }
      }
      throw new Error(`Unexpected support GET: ${url}`)
    }
  )
  vi.spyOn(api, 'post').mockImplementation(async (url) => {
    if (url !== '/api/support/tickets/7/read') {
      throw new Error(`Unexpected support POST: ${url}`)
    }
    unreadTicket.user_unread = false
    return { data: { success: true, message: '', data: null } }
  })

  const rendered = render(
    <QueryClientProvider client={client}>
      <SupportCenter />
    </QueryClientProvider>
  )
  try {
    const user = userEvent.setup()
    await screen.findByRole('button', { name: /Previously read ticket/ })
    await user.click(screen.getByRole('switch', { name: 'Unread only' }))
    await waitFor(() => {
      expect(
        screen.queryByRole('button', { name: /Previously read ticket/ })
      ).not.toBeInTheDocument()
    })

    await user.click(
      screen.getByRole('button', { name: /Audit unread support ticket/ })
    )
    await waitFor(() => {
      expect(
        screen.queryByRole('button', { name: /Audit unread support ticket/ })
      ).not.toBeInTheDocument()
    })

    await waitFor(() => {
      expect(
        screen.queryByText('The support answer must stay readable')
      ).toBeInTheDocument()
      expect(screen.queryByLabelText('Reply')).toBeInTheDocument()
    })
  } finally {
    rendered.unmount()
    client.clear()
  }
})

test('admin keeps the selected conversation when a status filter empties the list', async () => {
  const client = createClient()
  useAuthStore.getState().auth.setUser({ id: 299, username: 'admin', role: 10 })

  const ticket = {
    id: 9,
    user_id: 201,
    subject: 'Awaiting admin action',
    status: 'awaiting_user',
    user_unread: false,
    admin_unread: false,
    last_message_at: 2,
    created_at: 2,
    updated_at: 2,
  }

  vi.spyOn(api, 'get').mockImplementation(
    async (url, config: ApiRequestConfig = {}) => {
      if (url === '/api/admin/support/tickets') {
        const wantsResolved = config?.params?.status === 'resolved'
        const items = wantsResolved ? [] : [ticket]
        return {
          data: {
            success: true,
            message: '',
            data: {
              items: structuredClone(items),
              total: items.length,
              page: 1,
              page_size: 20,
              status_counts: {
                processing: 0,
                awaiting_user: items.length,
                resolved: 0,
              },
            },
          },
        }
      }
      if (url === '/api/admin/support/tickets/9') {
        return {
          data: {
            success: true,
            message: '',
            data: {
              ticket: structuredClone(ticket),
              messages: [
                {
                  id: 1,
                  ticket_id: 9,
                  author_role: 'user',
                  content: 'Admin detail must stay visible',
                  created_at: 2,
                },
              ],
            },
          },
        }
      }
      throw new Error(`Unexpected admin GET: ${url}`)
    }
  )

  const rendered = render(
    <QueryClientProvider client={client}>
      <SupportCenter />
    </QueryClientProvider>
  )
  try {
    const user = userEvent.setup()
    await user.click(
      await screen.findByRole('button', { name: /Awaiting admin action/ })
    )
    await screen.findByText('Admin detail must stay visible')

    await user.click(screen.getByRole('combobox', { name: 'Status' }))
    await user.click(screen.getByRole('option', { name: 'Resolved' }))

    await waitFor(() => {
      expect(
        screen.queryByRole('button', { name: /Awaiting admin action/ })
      ).not.toBeInTheDocument()
    })

    expect(
      screen.queryByText('Admin detail must stay visible')
    ).toBeInTheDocument()
    expect(screen.queryByLabelText('Reply')).toBeInTheDocument()
  } finally {
    rendered.unmount()
    client.clear()
  }
})
