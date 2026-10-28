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
import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import { useImageGeneration } from '../hooks/use-image-generation'

const mocks = vi.hoisted(() => ({
  loadFullTokenKey: vi.fn(),
  requestImageGeneration: vi.fn(),
}))

const loadFullTokenKeyMock = mocks.loadFullTokenKey
const requestImageGenerationMock = mocks.requestImageGeneration

vi.mock('../api', () => ({
  loadFullTokenKey: (...args: unknown[]) => mocks.loadFullTokenKey(...args),
  requestImageGeneration: (...args: unknown[]) =>
    mocks.requestImageGeneration(...args),
}))

vi.mock('@/lib/handle-server-error', () => ({
  handleServerError: vi.fn(),
}))

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

beforeEach(() => {
  loadFullTokenKeyMock.mockReset()
  requestImageGenerationMock.mockReset()
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('cancelling generation aborts both the key read and the image request', () => {
  test('cancel during the API key read passes the abort signal and reports cancellation', async () => {
    const keyGate = deferred<string>()
    let keySignal: AbortSignal | null = null
    loadFullTokenKeyMock.mockImplementation(
      (_id: number, signal?: AbortSignal) => {
        keySignal = signal ?? null
        signal?.addEventListener('abort', () => {
          keyGate.reject(new DOMException('aborted', 'AbortError'))
        })
        return keyGate.promise
      }
    )
    requestImageGenerationMock.mockResolvedValue([])

    const { result } = renderHook(() => useImageGeneration())
    let generatePromise: Promise<unknown> | null = null
    act(() => {
      generatePromise = result.current.generate({
        apiKeyId: 7,
        model: 'dall-e-3',
        prompt: 'a lake',
        count: 1,
        size: '1024x1024',
        quality: 'standard',
        style: 'vivid',
        images: [],
      })
    })

    await waitFor(() => expect(loadFullTokenKeyMock).toHaveBeenCalledTimes(1))
    expect(keySignal).toBeInstanceOf(AbortSignal)

    act(() => {
      result.current.cancel()
    })

    await act(async () => {
      await generatePromise
    })

    expect(requestImageGenerationMock).not.toHaveBeenCalled()
    expect(result.current.error).toBe('Generation was cancelled')
    expect(result.current.isGenerating).toBe(false)
  })

  test('cancel during the image request aborts the relay call', async () => {
    loadFullTokenKeyMock.mockResolvedValue('sk-test')
    const imageGate = deferred<[]>()
    let imageSignal: AbortSignal | null = null
    requestImageGenerationMock.mockImplementation(
      (params: { signal?: AbortSignal }) => {
        imageSignal = params.signal ?? null
        params.signal?.addEventListener('abort', () => {
          imageGate.reject(new DOMException('aborted', 'AbortError'))
        })
        return imageGate.promise
      }
    )

    const { result } = renderHook(() => useImageGeneration())
    let generatePromise: Promise<unknown> | null = null
    act(() => {
      generatePromise = result.current.generate({
        apiKeyId: 7,
        model: 'gpt-image-1',
        prompt: 'a city',
        count: 1,
        size: '1024x1024',
        quality: 'medium',
        style: '',
        images: [],
      })
    })

    await waitFor(() =>
      expect(requestImageGenerationMock).toHaveBeenCalledTimes(1)
    )
    expect(imageSignal).toBeInstanceOf(AbortSignal)

    act(() => {
      result.current.cancel()
    })

    await act(async () => {
      await generatePromise
    })

    expect(result.current.error).toBe('Generation was cancelled')
    expect(result.current.isGenerating).toBe(false)
  })

  test('unmounting while generating aborts the request and avoids state updates', async () => {
    const keyGate = deferred<string>()
    loadFullTokenKeyMock.mockImplementation(
      (_id: number, signal?: AbortSignal) => {
        signal?.addEventListener('abort', () => {
          keyGate.reject(new DOMException('aborted', 'AbortError'))
        })
        return keyGate.promise
      }
    )
    requestImageGenerationMock.mockResolvedValue([])

    const errorSpy = vi
      .spyOn(console, 'error')
      .mockImplementation(() => undefined)
    const { result, unmount } = renderHook(() => useImageGeneration())
    act(() => {
      void result.current.generate({
        apiKeyId: 7,
        model: 'dall-e-3',
        prompt: 'a lake',
        count: 1,
        size: '1024x1024',
        quality: 'standard',
        style: 'vivid',
        images: [],
      })
    })
    await waitFor(() => expect(loadFullTokenKeyMock).toHaveBeenCalledTimes(1))

    unmount()

    await act(async () => {
      await Promise.resolve()
    })

    expect(loadFullTokenKeyMock.mock.calls[0][1]).toBeInstanceOf(AbortSignal)
    expect((loadFullTokenKeyMock.mock.calls[0][1] as AbortSignal).aborted).toBe(
      true
    )
    errorSpy.mockRestore()
  })
})
