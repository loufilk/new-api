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
import {
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

import { SettingsPageProvider } from '../../components/settings-page-context'
import { GlobalSettingsCard } from '../global-settings-card'

let client: QueryClient

function Fixture(props: { owner: string }) {
  const [container, setContainer] = useState<HTMLDivElement | null>(null)
  return (
    <>
      <div ref={setContainer} />
      <SettingsPageProvider actionsContainer={container}>
        <GlobalSettingsCard
          defaultValues={{
            PublicModelOwnedBy: props.owner,
            global: {
              pass_through_request_enabled: false,
              thinking_model_blacklist: '[]',
              chat_completions_to_responses_policy: '{}',
            },
            general_setting: {
              ping_interval_enabled: false,
              ping_interval_seconds: 60,
            },
          }}
        />
      </SettingsPageProvider>
    </>
  )
}

function renderOwner(owner = 'chuanapi') {
  render(
    <QueryClientProvider client={client}>
      <Fixture owner={owner} />
    </QueryClientProvider>
  )
  return screen.getByRole('textbox', { name: 'Public model owner' })
}

beforeEach(() => {
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
})
afterEach(() => {
  cleanup()
  client.clear()
})

test('saving public owner trims whitespace and only updates the global owner option', async () => {
  const input = renderOwner()
  fireEvent.change(input, { target: { value: '  My public brand  ' } })
  await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() =>
    expect(api.put).toHaveBeenCalledExactlyOnceWith('/api/option/', {
      key: 'PublicModelOwnedBy',
      value: 'My public brand',
    })
  )
})

test('clearing a custom public owner saves the chuanapi fallback', async () => {
  const input = renderOwner('Custom brand')
  await userEvent.clear(input)
  await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() =>
    expect(api.put).toHaveBeenCalledWith('/api/option/', {
      key: 'PublicModelOwnedBy',
      value: 'chuanapi',
    })
  )
})

test.each(['x'.repeat(129), 'unsafe\u202eowner', 'hidden\u200bowner'])(
  'invalid public owner %s shows an accessible error and prevents saving',
  async (value) => {
    const input = renderOwner()
    fireEvent.change(input, { target: { value } })
    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    await waitFor(() => expect(input).toHaveAttribute('aria-invalid', 'true'))
    expect(api.put).not.toHaveBeenCalled()
  }
)

test('a pending public owner save disables the shared save action until the request finishes', async () => {
  let resolve: ((value: { data: { success: boolean } }) => void) | undefined
  vi.mocked(api.put).mockImplementationOnce(
    () =>
      new Promise((done) => {
        resolve = done
      })
  )
  const input = renderOwner()
  fireEvent.change(input, { target: { value: 'Pending brand' } })
  await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Saving...' })).toBeDisabled()
  )
  resolve?.({ data: { success: true } })
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save Changes' })).toBeEnabled()
  )
  expect(api.put).toHaveBeenCalledTimes(1)
})

test('a failed owner save preserves the draft and allows retry without an unhandled rejection', async () => {
  vi.mocked(api.put).mockResolvedValueOnce({
    data: { success: false, message: 'Save rejected' },
  })
  const input = renderOwner()
  fireEvent.change(input, { target: { value: 'Retry brand' } })
  await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save Changes' })).toBeEnabled()
  )
  expect(input).toHaveValue('Retry brand')
  await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(2))
})
