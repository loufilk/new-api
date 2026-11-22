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
import { renderHook, waitFor } from '@testing-library/react'
import type { PropsWithChildren } from 'react'
import { expect, test, vi } from 'vitest'

import { getUserGroups, getUserModels } from '../api'
import type { ModelOption } from '../types'
import { usePlaygroundOptions } from './use-playground-options'

vi.mock('../api', () => ({ getUserGroups: vi.fn(), getUserModels: vi.fn() }))

test('does not expose the previous group model while a new group is loading or has no models', async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const wrapper = ({ children }: PropsWithChildren) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
  vi.mocked(getUserGroups).mockResolvedValue(
    ['first', 'second'].map((value) => ({
      value,
      label: value,
      desc: value,
      ratio: 1,
    }))
  )
  let resolve!: (models: ModelOption[]) => void
  vi.mocked(getUserModels).mockImplementation((group) =>
    group === 'first'
      ? Promise.resolve([{ value: 'only-first', label: 'only-first' }])
      : new Promise<ModelOption[]>((done) => {
          resolve = done
        })
  )
  const updateConfig = vi.fn()
  const setGroups = vi.fn()
  const view = renderHook(
    ({ group }) =>
      usePlaygroundOptions({
        currentGroup: group,
        currentModel: 'only-first',
        setGroups,
        updateConfig,
      }),
    { wrapper, initialProps: { group: 'first' } }
  )
  await waitFor(() => expect(view.result.current.canSendSelection).toBe(true))
  view.rerender({ group: 'second' })
  expect(view.result.current.models).toEqual([])
  expect(view.result.current.canSendSelection).toBe(false)
  resolve([])
  await waitFor(() => expect(updateConfig).toHaveBeenCalledWith('model', ''))
  expect(view.result.current.canSendSelection).toBe(false)
  view.unmount()
  client.clear()
})
