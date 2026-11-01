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
import { act, renderHook, waitFor } from '@testing-library/react'
import type { PropsWithChildren } from 'react'
import { afterEach, expect, test, vi } from 'vitest'

import { useNotifications } from '@/hooks/use-notifications'
import { api } from '@/lib/api'

import { useUpdateOption } from '../use-update-option'

afterEach(() => localStorage.clear())

test.each(['console_setting.announcements', 'Notice'] as const)(
  'saving %s refreshes already mounted announcement consumers without waiting five minutes',
  async (key) => {
    const before = 'Previous announcement'
    const after = '![New image](https://images.example.test/a.png)'
    let content = before
    const client = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    })
    const status = () => ({
      announcements_enabled: true,
      announcements: [{ id: 1, content }],
    })
    client.setQueryData(['status'], status())
    client.setQueryData(['notice'], { success: true, data: content })
    vi.spyOn(api, 'get').mockImplementation(async (url) => ({
      data: { success: true, data: url === '/api/status' ? status() : content },
    }))
    vi.spyOn(api, 'put').mockImplementation(async () => {
      content = after
      return { data: { success: true } }
    })
    const wrapper = (props: PropsWithChildren) => (
      <QueryClientProvider client={client}>
        {props.children}
      </QueryClientProvider>
    )
    const { result } = renderHook(
      () => ({ update: useUpdateOption(), notifications: useNotifications() }),
      { wrapper }
    )
    expect(result.current.notifications.notice).toBe(before)
    await act(() =>
      result.current.update.mutateAsync({
        key,
        value:
          key === 'Notice'
            ? after
            : JSON.stringify([{ id: 1, content: after }]),
      })
    )
    await waitFor(() =>
      expect(
        key === 'Notice'
          ? result.current.notifications.notice
          : result.current.notifications.announcements[0].content
      ).toBe(after)
    )
  }
)
