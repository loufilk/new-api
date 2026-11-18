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
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { SidebarModulesCard } from '../sidebar-modules-card'

afterEach(() => {
  cleanup()
  useAuthStore.getState().auth.reset()
})

it('shows only console and personal settings when a legacy configuration enables chat tools', async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  client.setQueryData(['status'], {
    SidebarModulesAdmin: JSON.stringify({
      chat: { enabled: true, playground: true, image: true, chat: true },
    }),
  })
  useAuthStore.getState().auth.setUser({ id: 7, username: 'user', role: 1 })
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        sidebar_modules: JSON.stringify({
          chat: { enabled: true, playground: true, image: true, chat: true },
        }),
      },
    },
  })
  render(
    <QueryClientProvider client={client}>
      <SidebarModulesCard />
    </QueryClientProvider>
  )

  await waitFor(() => expect(get).toHaveBeenCalledWith('/api/user/self'))
  expect(screen.getByText('Console Area')).toBeVisible()
  expect(screen.getByText('Personal Center Area')).toBeVisible()
  expect(screen.getByText('API token management')).toBeVisible()
  for (const label of ['Chat Area', 'Text Generation', 'Image Generation']) {
    expect(screen.queryByText(label)).not.toBeInTheDocument()
  }
  client.clear()
})
