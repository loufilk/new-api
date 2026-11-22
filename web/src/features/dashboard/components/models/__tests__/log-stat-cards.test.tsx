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
import { act, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { LogStatCards } from '../log-stat-cards'

beforeEach(() => {
  localStorage.clear()
  useAuthStore.setState(useAuthStore.getInitialState(), true)
  useSystemConfigStore.setState(useSystemConfigStore.getInitialState(), true)
})
afterEach(() => {
  vi.restoreAllMocks()
  useAuthStore.setState(useAuthStore.getInitialState(), true)
  useSystemConfigStore.setState(useSystemConfigStore.getInitialState(), true)
  localStorage.clear()
})

describe('model dashboard cache cards', () => {
  test('exposes loading state and shows independent cache counters in the responsive seven-card layout', async () => {
    let resolveRequest!: (response: unknown) => void
    vi.spyOn(api, 'get').mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveRequest = resolve
        })
    )
    render(<LogStatCards />)
    const read = screen.getByRole('group', { name: 'Cache Read' })
    const write = screen.getByRole('group', { name: 'Cache Write' })
    expect(read).toHaveAttribute('aria-busy', 'true')
    expect(write).toHaveAttribute('aria-busy', 'true')
    expect(screen.getAllByRole('group')).toHaveLength(7)
    expect(read.parentElement).toHaveClass('grid-cols-2', 'lg:grid-cols-7')
    await act(async () =>
      resolveRequest({
        data: {
          success: true,
          data: [
            {
              created_at: 3600,
              token_used: 30,
              cache_read_tokens: 8,
              cache_creation_tokens: 5,
            },
          ],
        },
      })
    )
    await waitFor(() => expect(read).toHaveAttribute('aria-busy', 'false'))
    expect(within(read).getByText('8')).toBeVisible()
    expect(within(write).getByText('5')).toBeVisible()
    expect(
      within(screen.getByRole('group', { name: 'Total Tokens' })).getByText(
        '30'
      )
    ).toBeVisible()
  })

  test('shows unavailable cache counters rather than fabricated zero on request failure', async () => {
    vi.spyOn(api, 'get').mockRejectedValue(new Error('network unavailable'))
    render(<LogStatCards />)
    const read = screen.getByRole('group', { name: 'Cache Read' })
    await waitFor(() => expect(read).toHaveAttribute('aria-busy', 'false'))
    expect(within(read).getByText('--')).toBeVisible()
    expect(
      within(screen.getByRole('group', { name: 'Cache Write' })).getByText('--')
    ).toBeVisible()
  })

  test('ignores stale cache totals when a newer filter request finishes first', async () => {
    let resolveFirst!: (response: unknown) => void
    vi.spyOn(api, 'get')
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveFirst = resolve
          })
      )
      .mockResolvedValueOnce({
        data: {
          success: true,
          data: [
            {
              created_at: 7200,
              cache_read_tokens: 200,
              cache_creation_tokens: 300,
            },
          ],
        },
      })
    const view = render(
      <LogStatCards
        filters={{
          start_timestamp: new Date(3600_000),
          end_timestamp: new Date(7199_000),
        }}
      />
    )
    view.rerender(
      <LogStatCards
        filters={{
          start_timestamp: new Date(7200_000),
          end_timestamp: new Date(10799_000),
        }}
      />
    )
    const read = screen.getByRole('group', { name: 'Cache Read' })
    expect(await within(read).findByText('200')).toBeVisible()
    await act(async () =>
      resolveFirst({
        data: {
          success: true,
          data: [
            {
              created_at: 3600,
              cache_read_tokens: 99,
              cache_creation_tokens: 99,
            },
          ],
        },
      })
    )
    expect(within(read).getByText('200')).toBeVisible()
    expect(within(read).queryByText('99')).not.toBeInTheDocument()
  })
})
