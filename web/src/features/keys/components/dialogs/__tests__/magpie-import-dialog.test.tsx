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
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { apiKeySchema } from '../../../types'
import { ApiKeysProvider, useApiKeys } from '../../api-keys-provider'
import { MagpieImportDialog } from '../magpie-import-dialog'

const token = apiKeySchema.parse({
  id: 7,
  name: 'selected key',
  key: 'masked',
  status: 1,
  remain_quota: 1,
  used_quota: 0,
  unlimited_quota: false,
  expired_time: -1,
  created_time: 1,
  accessed_time: 1,
  group: 'default',
  model_limits_enabled: false,
})

function signIn(id: number) {
  useAuthStore.getState().auth.setUser({ id, username: 'fixture', role: 1 })
}

function Fixture() {
  const [open, setOpen] = useState(true)
  const keys = useApiKeys()
  return (
    <>
      <output aria-label='Cached key count'>
        {Object.keys(keys.resolvedKeys).length}
      </output>
      {open && (
        <MagpieImportDialog token={token} onClose={() => setOpen(false)} />
      )}
    </>
  )
}

function renderFixture() {
  return render(
    <ApiKeysProvider>
      <Fixture />
    </ApiKeysProvider>
  )
}

beforeEach(() => {
  signIn(3)
  vi.spyOn(navigator, 'userAgent', 'get').mockReturnValue('X11; Linux x86_64')
})

afterEach(() => {
  cleanup()
  vi.useRealTimers()
  useAuthStore.getState().auth.reset()
  localStorage.clear()
  sessionStorage.clear()
})

test('consumes only one owned Key on the explicit second action without persisting it', async () => {
  const raw = 'synthetic-for-this-import'
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue({ data: { success: true, data: { key: raw } } })
  const launch = vi.spyOn(window, 'open').mockReturnValue(null)
  const user = userEvent.setup()
  localStorage.setItem(
    'status',
    JSON.stringify({ server_address: 'https://untrusted.invalid' })
  )
  renderFixture()

  expect(screen.getByRole('dialog', { name: 'Add to Magpie' })).toBeVisible()
  expect(screen.getByRole('link', { name: 'Magpie help' })).toHaveAttribute(
    'href',
    'https://usemagpie.ai/docs/import'
  )
  expect(
    screen.queryByText('https://untrusted.invalid')
  ).not.toBeInTheDocument()
  expect(post).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Prepare import' }))
  const button = await screen.findByRole('button', { name: 'Open Magpie' })
  expect(post).toHaveBeenCalledExactlyOnceWith('/api/token/7/key', undefined, {
    signal: expect.any(AbortSignal),
  })
  expect(launch).not.toHaveBeenCalled()
  expect(screen.getByLabelText('Cached key count')).toHaveTextContent('0')
  expect(document.body.innerHTML).not.toContain(raw)
  expect(JSON.stringify(localStorage)).not.toContain(raw)
  expect(JSON.stringify(sessionStorage)).not.toContain(raw)

  await user.click(button)
  expect(launch).toHaveBeenCalledTimes(1)
  const [href, target, features] = launch.mock.calls[0]
  const result = new URL(href ?? '')
  expect(result.protocol).toBe('magpie:')
  expect(result.host).toBe('import')
  expect(result.searchParams.get('key')).toBe(`sk-${raw}`)
  expect(result.searchParams.get('anthropic')).toBe(window.location.origin)
  expect(target).toBe('_blank')
  expect(features).toBe('noopener,noreferrer')
  expect(
    screen.queryByRole('button', { name: 'Open Magpie' })
  ).not.toBeInTheDocument()
  expect(screen.getByRole('status', { name: '' })).toHaveTextContent(
    'Check your browser and Magpie for a confirmation.'
  )
  expect(screen.getByLabelText('Cached key count')).toHaveTextContent('0')
})

test('mobile help is reachable without making a Key reveal or protocol launch available', () => {
  vi.spyOn(navigator, 'userAgent', 'get').mockReturnValue(
    'Mozilla/5.0 (iPhone)'
  )
  const post = vi.spyOn(api, 'post')
  const launch = vi.spyOn(window, 'open')
  renderFixture()
  expect(
    screen.getByText(/desktop browser with Magpie installed/)
  ).toBeVisible()
  expect(screen.getByRole('link', { name: 'Magpie help' })).toHaveAttribute(
    'rel',
    'noopener noreferrer'
  )
  expect(
    screen.queryByRole('button', { name: 'Prepare import' })
  ).not.toBeInTheDocument()
  expect(post).not.toHaveBeenCalled()
  expect(launch).not.toHaveBeenCalled()
})

test('cancelling a pending request aborts the Key reveal and suppresses late credentials', async () => {
  let resolve: (value: { data: unknown }) => void = () => {}
  const response = new Promise<{ data: unknown }>((complete) => {
    resolve = complete
  })
  const post = vi.spyOn(api, 'post').mockReturnValue(response)
  const launch = vi.spyOn(window, 'open').mockReturnValue(null)
  const user = userEvent.setup()
  renderFixture()
  await user.click(screen.getByRole('button', { name: 'Prepare import' }))
  const signal = post.mock.calls[0]?.[2]?.signal
  expect(signal?.aborted).toBe(false)
  await user.keyboard('{Escape}')
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(signal?.aborted).toBe(true)
  await act(async () =>
    resolve({
      data: { success: true, data: { key: 'never-launch-old-fixture' } },
    })
  )
  expect(launch).not.toHaveBeenCalled()
  expect(screen.getByLabelText('Cached key count')).toHaveTextContent('0')
})

test('changing the signed-in owner invalidates a prepared launch', async () => {
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: { key: 'old-owner-only' } },
  })
  const launch = vi.spyOn(window, 'open').mockReturnValue(null)
  renderFixture()
  fireEvent.click(screen.getByRole('button', { name: 'Prepare import' }))
  const oldButton = await screen.findByRole('button', { name: 'Open Magpie' })
  act(() => signIn(8))
  fireEvent.click(oldButton)
  expect(
    screen.queryByRole('button', { name: 'Open Magpie' })
  ).not.toBeInTheDocument()
  expect(post).toHaveBeenCalledTimes(1)
  expect(launch).not.toHaveBeenCalled()
  expect(screen.getByLabelText('Cached key count')).toHaveTextContent('0')
})

test('the prepared local launch expires and can only be retried with a fresh reveal', async () => {
  vi.useFakeTimers()
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: { key: 'expires-in-memory' } },
  })
  const launch = vi.spyOn(window, 'open').mockReturnValue(null)
  renderFixture()
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name: 'Prepare import' }))
  })
  expect(screen.getByRole('button', { name: 'Open Magpie' })).toBeVisible()
  act(() => vi.advanceTimersByTime(60_000))
  expect(screen.getByText(/The import expired/)).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Open Magpie' })
  ).not.toBeInTheDocument()
  expect(launch).not.toHaveBeenCalled()
  expect(post).toHaveBeenCalledTimes(1)
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name: 'Prepare import' }))
  })
  expect(post).toHaveBeenCalledTimes(2)
  expect(launch).not.toHaveBeenCalled()
})

test('a denied reveal shows a fixed error and never offers launch or echoes the server body', async () => {
  const sentinel = 'synthetic-do-not-echo'
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue({ data: { success: false, message: sentinel } })
  const launch = vi.spyOn(window, 'open').mockReturnValue(null)
  renderFixture()
  fireEvent.click(screen.getByRole('button', { name: 'Prepare import' }))
  await waitFor(() =>
    expect(screen.getByText(/Could not prepare or open/)).toBeVisible()
  )
  expect(post).toHaveBeenCalledTimes(1)
  expect(screen.queryByText(sentinel)).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Open Magpie' })
  ).not.toBeInTheDocument()
  expect(launch).not.toHaveBeenCalled()
})
