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
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { SidebarModulesCard } from '../sidebar-modules-card'

afterEach(() => {
  cleanup()
  useAuthStore.getState().auth.reset()
})

async function renderSidebarSettings(
  adminChat = { enabled: true, playground: true, image: true, chat: true },
  userChat = { enabled: true, playground: true, image: true, chat: true }
) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  client.setQueryData(['status'], {
    SidebarModulesAdmin: JSON.stringify({
      chat: adminChat,
    }),
  })
  useAuthStore.getState().auth.setUser({ id: 7, username: 'user', role: 1 })
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        sidebar_modules: JSON.stringify({
          chat: userChat,
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
  return client
}

it('shows native generation switches with console and personal settings when administrators enable them', async () => {
  const client = await renderSidebarSettings()
  expect(screen.getByText('Console Area')).toBeVisible()
  expect(screen.getByText('Personal Center Area')).toBeVisible()
  expect(screen.getByText('API token management')).toBeVisible()
  expect(screen.getByRole('switch', { name: 'Chat Area' })).toBeChecked()
  expect(screen.getByRole('switch', { name: 'Text Generation' })).toBeChecked()
  expect(screen.getByRole('switch', { name: 'Image Generation' })).toBeChecked()
  expect(screen.queryByText('Chat session management')).not.toBeInTheDocument()
  client.clear()
})

it('lets users re-enable previously hidden native generation entries and saves both toggles', async () => {
  const client = await renderSidebarSettings(undefined, {
    enabled: true,
    playground: false,
    image: false,
    chat: false,
  })
  const save = vi.spyOn(api, 'put').mockResolvedValue({
    data: { success: true },
  })
  const textSwitch = screen.getByRole('switch', { name: 'Text Generation' })
  const imageSwitch = screen.getByRole('switch', { name: 'Image Generation' })
  await waitFor(() => expect(textSwitch).not.toBeChecked())
  expect(imageSwitch).not.toBeChecked()
  textSwitch.focus()
  await userEvent.keyboard('[Space]')
  await userEvent.click(imageSwitch)
  await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))

  const expectedConfig = JSON.stringify({
    chat: { enabled: true, playground: true, image: true, chat: false },
  })
  await waitFor(() =>
    expect(save).toHaveBeenCalledWith('/api/user/self', {
      sidebar_modules: expectedConfig,
    })
  )
  expect(useAuthStore.getState().auth.user?.sidebar_modules).toBe(
    expectedConfig
  )
  client.clear()
})

it('does not offer a personal image toggle when administrators disable image generation', async () => {
  const client = await renderSidebarSettings({
    enabled: true,
    playground: true,
    image: false,
    chat: true,
  })
  expect(screen.getByRole('switch', { name: 'Text Generation' })).toBeChecked()
  expect(
    screen.queryByRole('switch', { name: 'Image Generation' })
  ).not.toBeInTheDocument()
  client.clear()
})
