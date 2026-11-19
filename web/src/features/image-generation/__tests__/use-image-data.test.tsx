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
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'

import { loadImagePageBootstrap, loadImageTokenModels } from '../api'
import { useImageData } from '../hooks/use-image-data'
import type { ImageApiKeyOption } from '../types'
import { signInHistoryOwner } from './history-fixture'

vi.mock('../api', () => ({
  loadImageTokenModels: vi.fn(),
  loadImagePageBootstrap: vi.fn(),
}))

const bootstrapMock = vi.mocked(loadImagePageBootstrap)
const tokenModelsMock = vi.mocked(loadImageTokenModels)

function makeKey(id: number, group: string): ImageApiKeyOption {
  return {
    id,
    name: `key-${id}`,
    group,
    model_limits_enabled: false,
    model_limits: '',
    status: 1,
  }
}

const PRICING = [
  { model_name: 'dall-e-3', supported_endpoint_types: ['image-generation'] },
  { model_name: 'gpt-image-2', supported_endpoint_types: ['image-generation'] },
  { model_name: 'gpt-4o', supported_endpoint_types: ['chat'] },
]

let bootstrapKeys: ImageApiKeyOption[] = []

function createWrapper() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return function Wrapper(props: { children: ReactNode }) {
    return (
      <QueryClientProvider client={client}>
        {props.children}
      </QueryClientProvider>
    )
  }
}

beforeEach(() => {
  vi.resetAllMocks()
  localStorage.clear()
  signInHistoryOwner()
  bootstrapKeys = [makeKey(1, 'g1')]
  bootstrapMock.mockImplementation(async () => ({
    keys: bootstrapKeys,
    pricingModels: PRICING,
  }))
  tokenModelsMock.mockImplementation(async (id: number) => {
    const models: Record<number, string[]> = {
      1: ['dall-e-3', 'gpt-image-2'],
      2: ['gpt-image-2'],
      3: [],
      10: ['dall-e-3'],
      11: ['gpt-image-2'],
    }
    return models[id] ?? []
  })
})

afterEach(() => {
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
  localStorage.clear()
})

describe('useImageData selects keys and models as availability changes', () => {
  test('keeps two auto tokens with different routing chains in separate model scopes', async () => {
    bootstrapKeys = [
      { ...makeKey(10, 'auto'), auto_groups: ['image-first'] },
      { ...makeKey(11, 'auto'), auto_groups: ['image-second'] },
    ]
    const { result } = renderHook(() => useImageData(), {
      wrapper: createWrapper(),
    })

    await waitFor(() =>
      expect(result.current.availableModels).toEqual(['dall-e-3'])
    )
    act(() => result.current.setApiKeyId(11))
    await waitFor(() =>
      expect(result.current.availableModels).toEqual(['gpt-image-2'])
    )
    expect(tokenModelsMock).toHaveBeenCalledWith(10)
    expect(tokenModelsMock).toHaveBeenCalledWith(11)
  })

  test('clears cached models when revalidating the selected token fails', async () => {
    const { result } = renderHook(() => useImageData(), {
      wrapper: createWrapper(),
    })
    await waitFor(() => expect(result.current.model).toBe('dall-e-3'))
    tokenModelsMock.mockRejectedValue(new Error('Token access revoked'))

    await act(async () => {
      await result.current.refetchTokenModels()
    })

    await waitFor(() => expect(result.current.isTokenModelsError).toBe(true))
    expect(result.current.availableModels).toEqual([])
    expect(result.current.model).toBe('')
  })

  test('requests the owned token models even when the selected key has an empty group', async () => {
    bootstrapKeys = [makeKey(2, '')]
    const { result } = renderHook(() => useImageData(), {
      wrapper: createWrapper(),
    })

    await waitFor(() => expect(result.current.apiKeyId).toBe(2))
    await waitFor(() => expect(tokenModelsMock).toHaveBeenCalledWith(2))
    await waitFor(() =>
      expect(result.current.availableModels).toEqual(['gpt-image-2'])
    )
  })

  test('selects the first key again when the current key disappears after refresh', async () => {
    bootstrapKeys = [makeKey(1, 'g1'), makeKey(2, '')]
    const { result } = renderHook(() => useImageData(), {
      wrapper: createWrapper(),
    })
    await waitFor(() => expect(result.current.apiKeyId).toBe(1))

    bootstrapKeys = [makeKey(2, '')]
    await act(async () => {
      await result.current.refetchBootstrap()
    })

    await waitFor(() => expect(result.current.apiKeyId).toBe(2))
  })

  test('moves the model to the first available option after the key changes', async () => {
    bootstrapKeys = [makeKey(1, 'g1'), makeKey(2, '')]
    const { result } = renderHook(() => useImageData(), {
      wrapper: createWrapper(),
    })
    await waitFor(() => expect(result.current.model).toBe('dall-e-3'))

    act(() => {
      result.current.setApiKeyId(2)
    })

    await waitFor(() => expect(result.current.model).toBe('gpt-image-2'))
  })

  test('clears the model when the new key offers no image models', async () => {
    bootstrapKeys = [makeKey(1, 'g1'), makeKey(3, 'g2')]
    const { result } = renderHook(() => useImageData(), {
      wrapper: createWrapper(),
    })
    await waitFor(() => expect(result.current.model).toBe('dall-e-3'))

    act(() => {
      result.current.setApiKeyId(3)
    })

    await waitFor(() => expect(result.current.model).toBe(''))
  })
})

test('reopening the image page remembers the owned key explicitly selected last time', async () => {
  bootstrapKeys = [makeKey(1, 'g1'), makeKey(2, 'g2')]
  const first = renderHook(() => useImageData(), { wrapper: createWrapper() })
  await waitFor(() => expect(first.result.current.model).toBe('dall-e-3'))
  act(() => first.result.current.setApiKeyId(2))
  await waitFor(() => expect(first.result.current.model).toBe('gpt-image-2'))
  first.unmount()

  const reopened = renderHook(() => useImageData(), {
    wrapper: createWrapper(),
  })
  await waitFor(() => expect(reopened.result.current.apiKeyId).toBe(2))
  await waitFor(() => expect(reopened.result.current.model).toBe('gpt-image-2'))
})

test('a saved key removed from the active list is not queried on reopening', async () => {
  bootstrapKeys = [makeKey(1, 'g1'), makeKey(2, 'g2')]
  const first = renderHook(() => useImageData(), { wrapper: createWrapper() })
  await waitFor(() => expect(first.result.current.apiKeyId).toBe(1))
  act(() => first.result.current.setApiKeyId(2))
  await waitFor(() => expect(first.result.current.model).toBe('gpt-image-2'))
  first.unmount()
  bootstrapKeys = [makeKey(1, 'g1')]
  tokenModelsMock.mockClear()

  const reopened = renderHook(() => useImageData(), {
    wrapper: createWrapper(),
  })
  await waitFor(() => expect(reopened.result.current.model).toBe('dall-e-3'))
  expect(reopened.result.current.apiKeyId).toBe(1)
  expect(tokenModelsMock).not.toHaveBeenCalledWith(2)
})

test('switching accounts keeps remembered key choices separate', async () => {
  bootstrapKeys = [makeKey(1, 'g1'), makeKey(2, 'g2')]
  const first = renderHook(() => useImageData(), { wrapper: createWrapper() })
  await waitFor(() => expect(first.result.current.apiKeyId).toBe(1))
  act(() => first.result.current.setApiKeyId(2))
  await waitFor(() => expect(first.result.current.model).toBe('gpt-image-2'))
  first.unmount()

  signInHistoryOwner(202)
  const other = renderHook(() => useImageData(), { wrapper: createWrapper() })
  await waitFor(() => expect(other.result.current.apiKeyId).toBe(1))
  other.unmount()

  signInHistoryOwner(101)
  const returned = renderHook(() => useImageData(), {
    wrapper: createWrapper(),
  })
  await waitFor(() => expect(returned.result.current.apiKeyId).toBe(2))
})

test('unavailable browser storage does not prevent selecting and loading an image key', async () => {
  vi.spyOn(window.localStorage, 'getItem').mockImplementation(() => {
    throw new DOMException('Storage blocked', 'SecurityError')
  })
  vi.spyOn(window.localStorage, 'setItem').mockImplementation(() => {
    throw new DOMException('Storage full', 'QuotaExceededError')
  })
  bootstrapKeys = [makeKey(1, 'g1'), makeKey(2, 'g2')]
  const { result } = renderHook(() => useImageData(), {
    wrapper: createWrapper(),
  })
  await waitFor(() => expect(result.current.model).toBe('dall-e-3'))
  act(() => result.current.setApiKeyId(2))
  await waitFor(() => expect(result.current.model).toBe('gpt-image-2'))
})

test('changing smart routing reloads effective models even when the saved manual group is unchanged', async () => {
  bootstrapKeys = [{ ...makeKey(1, 'g1'), smart_routing: false }]
  const { result } = renderHook(() => useImageData(), {
    wrapper: createWrapper(),
  })
  await waitFor(() =>
    expect(result.current.availableModels).toEqual(['dall-e-3', 'gpt-image-2'])
  )
  tokenModelsMock.mockResolvedValue(['gpt-image-2'])
  bootstrapKeys = [{ ...makeKey(1, 'g1'), smart_routing: true }]
  await act(async () => {
    await result.current.refetchBootstrap()
  })
  await waitFor(() =>
    expect(result.current.availableModels).toEqual(['gpt-image-2'])
  )
})

test('a remembered key is not selected when the account has no active keys', async () => {
  bootstrapKeys = [makeKey(1, 'g1'), makeKey(2, 'g2')]
  const first = renderHook(() => useImageData(), { wrapper: createWrapper() })
  await waitFor(() => expect(first.result.current.apiKeyId).toBe(1))
  act(() => first.result.current.setApiKeyId(2))
  await waitFor(() => expect(first.result.current.model).toBe('gpt-image-2'))
  first.unmount()
  bootstrapKeys = []
  tokenModelsMock.mockClear()

  const reopened = renderHook(() => useImageData(), {
    wrapper: createWrapper(),
  })
  await waitFor(() =>
    expect(reopened.result.current.isLoadingBootstrap).toBe(false)
  )
  expect(reopened.result.current.apiKeyId).toBeNull()
  expect(reopened.result.current.selectedKey).toBeNull()
  expect(tokenModelsMock).not.toHaveBeenCalled()
})
