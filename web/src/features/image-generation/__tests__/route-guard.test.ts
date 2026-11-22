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
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import { getStatus } from '@/lib/api'
import {
  getFreshSidebarModuleEnabled,
  isSidebarModuleEnabled,
} from '@/lib/nav-modules'
import { Route as ImageRoute } from '@/routes/_authenticated/image/index'

vi.mock('@/lib/api', async (importOriginal) => {
  const original = await importOriginal<typeof import('@/lib/api')>()
  return { ...original, getStatus: vi.fn() }
})

const mockedGetStatus = vi.mocked(getStatus)

function writeStatus(value: unknown) {
  window.localStorage.setItem('status', JSON.stringify(value))
}

function sidebarStatus(imageEnabled: boolean) {
  return {
    SidebarModulesAdmin: JSON.stringify({
      chat: { enabled: true, playground: true, image: imageEnabled },
    }),
  }
}

function createTestQueryClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
}

async function callImageBeforeLoad(queryClient: QueryClient): Promise<unknown> {
  const beforeLoad = ImageRoute.options.beforeLoad as unknown as (
    args: unknown
  ) => unknown
  return (beforeLoad as (args: unknown) => unknown)({
    context: { queryClient },
  })
}

beforeEach(() => {
  window.localStorage.clear()
  mockedGetStatus.mockReset()
})

afterEach(() => {
  window.localStorage.clear()
})

describe('image route gate uses shared status query', () => {
  test('redirects to dashboard when fresh status disables chat.image even if cache is stale', async () => {
    const queryClient = createTestQueryClient()
    writeStatus(sidebarStatus(true))
    expect(isSidebarModuleEnabled('chat', 'image')).toBe(true)
    mockedGetStatus.mockResolvedValue(sidebarStatus(false))

    let thrown: unknown
    try {
      await callImageBeforeLoad(queryClient)
    } catch (error) {
      thrown = error
    } finally {
      queryClient.clear()
    }

    expect(mockedGetStatus).toHaveBeenCalledTimes(1)
    expect(isRedirect(thrown)).toBe(true)
    expect((thrown as { options?: { to?: string } }).options?.to).toBe(
      '/dashboard'
    )
  })

  test('allows /image when fresh status enables chat.image despite a disabled cache', async () => {
    const queryClient = createTestQueryClient()
    writeStatus(sidebarStatus(false))
    mockedGetStatus.mockResolvedValue(sidebarStatus(true))

    try {
      await expect(callImageBeforeLoad(queryClient)).resolves.toBeUndefined()
    } finally {
      queryClient.clear()
    }
    expect(JSON.parse(window.localStorage.getItem('status') ?? '{}')).toEqual(
      sidebarStatus(true)
    )
  })

  test('redirects when cached status is corrupt and fresh fetch fails', async () => {
    const queryClient = createTestQueryClient()
    window.localStorage.setItem('status', 'not-json{{{')
    mockedGetStatus.mockRejectedValue(new Error('network down'))

    let thrown: unknown
    try {
      await callImageBeforeLoad(queryClient)
    } catch (error) {
      thrown = error
    } finally {
      queryClient.clear()
    }

    expect(isRedirect(thrown)).toBe(true)
    expect((thrown as { options?: { to?: string } }).options?.to).toBe(
      '/dashboard'
    )
  })

  test('redirects when no cached status exists and fresh fetch fails', async () => {
    const queryClient = createTestQueryClient()
    mockedGetStatus.mockRejectedValue(new Error('network down'))

    let thrown: unknown
    try {
      await callImageBeforeLoad(queryClient)
    } catch (error) {
      thrown = error
    } finally {
      queryClient.clear()
    }

    expect(isRedirect(thrown)).toBe(true)
    expect((thrown as { options?: { to?: string } }).options?.to).toBe(
      '/dashboard'
    )
  })

  test('falls back to cached disabled value when the status request fails', async () => {
    const queryClient = createTestQueryClient()
    writeStatus(sidebarStatus(false))
    mockedGetStatus.mockRejectedValue(new Error('network down'))

    let thrown: unknown
    try {
      await callImageBeforeLoad(queryClient)
    } catch (error) {
      thrown = error
    } finally {
      queryClient.clear()
    }

    expect(isRedirect(thrown)).toBe(true)
    expect((thrown as { options?: { to?: string } }).options?.to).toBe(
      '/dashboard'
    )
  })

  test('shared status query refreshes the cache for the next cached read', async () => {
    const queryClient = createTestQueryClient()
    writeStatus(sidebarStatus(true))
    mockedGetStatus.mockResolvedValue(sidebarStatus(false))

    try {
      expect(
        await getFreshSidebarModuleEnabled(queryClient, 'chat', 'image')
      ).toBe(false)
    } finally {
      queryClient.clear()
    }
    expect(isSidebarModuleEnabled('chat', 'image')).toBe(false)
  })

  test('reuses a fresh shared query without issuing another status request', async () => {
    const queryClient = createTestQueryClient()
    writeStatus(sidebarStatus(true))
    mockedGetStatus.mockResolvedValue(sidebarStatus(true))

    try {
      await callImageBeforeLoad(queryClient)
      expect(mockedGetStatus).toHaveBeenCalledTimes(1)
      mockedGetStatus.mockClear()
      await expect(callImageBeforeLoad(queryClient)).resolves.toBeUndefined()
      expect(mockedGetStatus).not.toHaveBeenCalled()
    } finally {
      queryClient.clear()
    }
  })
})
