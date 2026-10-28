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
import { toast } from 'sonner'
import { afterEach, expect, test, vi } from 'vitest'

import { SupportCenter } from '@/features/support'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

afterEach(() => {
  useAuthStore.getState().auth.reset()
})

test('a failed read marker displays the error fallback instead of mutation variables', async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  useAuthStore.getState().auth.setUser({ id: 201, username: 'user', role: 1 })
  const ticket = {
    id: 7,
    user_id: 201,
    subject: 'Unread support answer',
    status: 'awaiting_user',
    user_unread: true,
    admin_unread: false,
    last_message_at: 1,
    created_at: 1,
    updated_at: 1,
  }
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/support/tickets') {
      return {
        data: {
          success: true,
          message: '',
          data: {
            items: [ticket],
            total: 1,
            page: 1,
            page_size: 20,
            status_counts: { processing: 0, awaiting_user: 1, resolved: 0 },
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
            ticket,
            messages: [
              {
                id: 1,
                ticket_id: 7,
                author_role: 'admin',
                content: 'The reply remains visible after a read failure',
                created_at: 1,
              },
            ],
          },
        },
      }
    }
    throw new Error(`Unexpected support GET: ${url}`)
  })
  vi.spyOn(api, 'post').mockRejectedValue(new Error(''))
  const errorToast = vi.spyOn(toast, 'error').mockReturnValue('support-error')
  const rendered = render(
    <QueryClientProvider client={client}>
      <SupportCenter />
    </QueryClientProvider>
  )
  try {
    await userEvent
      .setup()
      .click(
        await screen.findByRole('button', { name: /Unread support answer/ })
      )
    await waitFor(() =>
      expect(errorToast).toHaveBeenCalledWith('Something went wrong!')
    )
    expect(
      screen.getByText('The reply remains visible after a read failure')
    ).toBeInTheDocument()
    expect(errorToast).toHaveBeenCalledTimes(1)
  } finally {
    rendered.unmount()
    client.clear()
  }
})
