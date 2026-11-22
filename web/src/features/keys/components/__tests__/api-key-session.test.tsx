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
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { ApiKeysProvider, useApiKeys } from '../api-keys-provider'

function KeyReader(props: {
  onResolved: (key: string | null | Record<number, string>) => void
}) {
  const keys = useApiKeys()
  return (
    <>
      <button
        type='button'
        onClick={async () => props.onResolved(await keys.resolveRealKey(7))}
      >
        Reveal key
      </button>
      <button
        type='button'
        onClick={async () =>
          props.onResolved(await keys.resolveRealKeysBatch([7]))
        }
      >
        Reveal batch
      </button>
      <output aria-label='Cached key'>{keys.resolvedKeys[7] ?? 'empty'}</output>
    </>
  )
}

function signIn(id: number) {
  useAuthStore.getState().auth.setUser({
    id,
    username: `fixture-${id}`,
    role: 1,
    group: 'fixture',
  })
}

beforeEach(() => signIn(7))

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
  localStorage.clear()
})

test.each(['another owner', 'a new session for the same owner'])(
  'revealed key cache is cleared for %s',
  async (transition) => {
    const post = vi.spyOn(api, 'post').mockResolvedValue({
      data: { success: true, data: { key: 'previous-session-fixture' } },
    })
    const onResolved = vi.fn()
    render(
      <ApiKeysProvider>
        <KeyReader onResolved={onResolved} />
      </ApiKeysProvider>
    )
    fireEvent.click(screen.getByRole('button', { name: 'Reveal key' }))
    await waitFor(() =>
      expect(screen.getByLabelText('Cached key')).toHaveTextContent(
        'sk-previous-session-fixture'
      )
    )

    act(() => {
      if (transition === 'another owner') signIn(8)
      else {
        useAuthStore.getState().auth.reset()
        signIn(7)
      }
    })

    expect(screen.getByLabelText('Cached key')).toHaveTextContent('empty')
    post.mockResolvedValue({
      data: { success: true, data: { key: 'current-session-fixture' } },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Reveal key' }))
    await waitFor(() =>
      expect(onResolved).toHaveBeenLastCalledWith('sk-current-session-fixture')
    )
    expect(post).toHaveBeenCalledTimes(2)
  }
)

test.each(['Reveal key', 'Reveal batch'])(
  '%s does not return credentials after leaving the page',
  async (action) => {
    let resolveKey: (() => void) | undefined
    const pending = new Promise<{ data: unknown }>((resolve) => {
      resolveKey = () =>
        resolve({
          data: {
            success: true,
            data: { key: 'late-fixture', keys: { 7: 'late-fixture' } },
          },
        })
    })
    const post = vi.spyOn(api, 'post').mockReturnValue(pending)
    const onResolved = vi.fn()
    const view = render(
      <ApiKeysProvider>
        <KeyReader onResolved={onResolved} />
      </ApiKeysProvider>
    )
    fireEvent.click(screen.getByRole('button', { name: action }))
    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    view.unmount()

    await act(async () => {
      resolveKey?.()
      await pending
    })
    expect(onResolved).toHaveBeenCalledWith(action === 'Reveal key' ? null : {})
  }
)
