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
import { QueryClient } from '@tanstack/react-query'
import { isRedirect } from '@tanstack/react-router'
import { renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import {
  SIDEBAR_MODULES_DEFAULT,
  parseSidebarModulesAdmin,
} from '@/features/system-settings/maintenance/config'
import { useSidebarData } from '@/hooks/use-sidebar-data'
import { isSidebarModuleEnabled } from '@/lib/nav-modules'
import { Route as ImageRoute } from '@/routes/_authenticated/image/index'

vi.mock('@/lib/api', async (importOriginal) => {
  const original = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...original,
    getStatus: vi.fn(async () => {
      throw new Error('network unavailable in unit test')
    }),
  }
})

async function callImageBeforeLoad(): Promise<unknown> {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const beforeLoad = ImageRoute.options.beforeLoad as unknown as (
    args: unknown
  ) => unknown
  try {
    return await beforeLoad({ context: { queryClient } })
  } finally {
    queryClient.clear()
  }
}

beforeEach(() => {
  vi.clearAllMocks()
})

function setSidebarModulesAdmin(value: unknown) {
  window.localStorage.setItem(
    'status',
    JSON.stringify({ SidebarModulesAdmin: JSON.stringify(value) })
  )
}

afterEach(() => {
  window.localStorage.removeItem('status')
})

describe('sidebar exposes Text Generation and Image Generation in order', () => {
  test('chat group lists text generation before image generation', () => {
    const { result } = renderHook(() => useSidebarData())
    const chat = result.current.navGroups.find((group) => group.id === 'chat')
    const urls = (chat?.items ?? [])
      .filter((item) => 'url' in item)
      .map((item) => (item as { url: string }).url)
    const playgroundIndex = urls.indexOf('/playground')
    const imageIndex = urls.indexOf('/image')
    expect(playgroundIndex).toBeGreaterThanOrEqual(0)
    expect(imageIndex).toBeGreaterThan(playgroundIndex)

    const titles = (chat?.items ?? [])
      .filter((item) => 'url' in item)
      .map((item) => (item as { title: string }).title)
    expect(titles).toContain('Text Generation')
    expect(titles).toContain('Image Generation')
    expect(titles).not.toContain('Playground')
  })

  test('admin chat.image defaults to true and stays independently controllable', () => {
    expect(SIDEBAR_MODULES_DEFAULT.chat.image).toBe(true)
    const parsed = parseSidebarModulesAdmin(
      JSON.stringify({
        chat: { enabled: true, playground: true, image: false, chat: true },
      })
    )
    expect(parsed.chat.image).toBe(false)
    expect(parsed.chat.playground).toBe(true)
  })
})

describe('chat.image route gate honors SidebarModulesAdmin', () => {
  test('disabled image module hides /image while playground stays visible', () => {
    setSidebarModulesAdmin({
      chat: { enabled: true, playground: true, image: false },
    })
    expect(isSidebarModuleEnabled('chat', 'image')).toBe(false)
    expect(isSidebarModuleEnabled('chat', 'playground')).toBe(true)
  })

  test('disabled image module redirects /image to /dashboard without legacy hops', async () => {
    setSidebarModulesAdmin({
      chat: { enabled: true, playground: true, image: false },
    })
    let thrown: unknown
    try {
      await callImageBeforeLoad()
    } catch (error) {
      thrown = error
    }
    expect(thrown).toBeDefined()
    expect(isRedirect(thrown)).toBe(true)
    expect((thrown as { options?: { to?: string } }).options?.to).toBe(
      '/dashboard'
    )
  })

  test('enabled image module lets /image beforeLoad pass without redirect', async () => {
    setSidebarModulesAdmin({
      chat: { enabled: true, playground: true, image: true },
    })
    await expect(callImageBeforeLoad()).resolves.toBeUndefined()
  })

  test('missing sidebar config redirects /image when status is unavailable', async () => {
    let thrown: unknown
    try {
      await callImageBeforeLoad()
    } catch (error) {
      thrown = error
    }

    expect(isRedirect(thrown)).toBe(true)
    expect((thrown as { options?: { to?: string } }).options?.to).toBe(
      '/dashboard'
    )
  })
})
