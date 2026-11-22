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
  focusManager,
  QueryClient,
  QueryClientProvider,
} from '@tanstack/react-query'
import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import zh from '@/i18n/locales/zh.json'
import { api } from '@/lib/api'

import { AccountPoolDashboard } from '../index'
import type { PoolSnapshot } from '../types'

function snapshot(): PoolSnapshot {
  return {
    configured: true,
    state: 'ready',
    updated_at: 1800000000,
    total: 21,
    groups: [
      {
        name: 'Shared',
        ratio: 1.5,
        total: 21,
        available: 20,
        concurrency: 3,
        concurrency_limit: 3,
      },
    ],
    items: [
      {
        anonymous_id: 'AC-0123abcd',
        provider: 'openai',
        auth_type: 'oauth',
        plan: 'plus',
        status: 'available',
        concurrency: 3,
        concurrency_limit: 3,
        groups: [{ name: 'Shared', ratio: 1.5 }],
        usage_5h_percent: null,
        usage_7d_percent: 45,
        last_used_at: null,
      },
    ],
  }
}
function renderPool(
  enabled: boolean | undefined = true,
  cached?: PoolSnapshot
) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  client.setQueryData(['status'], { account_pool_enabled: enabled })
  if (cached) {
    client.setQueryData(
      [
        'account-pool',
        'snapshot',
        { q: '', type: '', status: '', group: '', page: 1, page_size: 20 },
      ],
      cached
    )
  }
  const view = render(
    <QueryClientProvider client={client}>
      <AccountPoolDashboard />
    </QueryClientProvider>
  )
  return { client, ...view }
}
beforeEach(async () => {
  i18next.addResourceBundle('en', 'translation', en.translation, true, true)
  i18next.addResourceBundle('zhCN', 'translation', zh.translation, true, true)
  await i18next.changeLanguage('en')
})
describe('account pool', () => {
  test('shows all-filter aggregates while full concurrency stays available and missing usage stays unknown', async () => {
    const get = vi
      .spyOn(api, 'get')
      .mockResolvedValue({ data: { success: true, data: snapshot() } })
    renderPool()
    expect(await screen.findByText('AC-0123abcd')).toBeVisible()
    expect(screen.getByText(/^Last updated:/)).toBeVisible()
    expect(screen.getByText('Available 20 / 21 accounts')).toBeVisible()
    const table = screen.getByRole('region', { name: 'Anonymous account list' })
    expect(within(table).getByText('Available')).toBeVisible()
    expect(within(table).getAllByRole('progressbar')).toHaveLength(1)
    expect(
      within(table).getByRole('progressbar', { name: '7-day usage' })
    ).toHaveAttribute('aria-valuenow', '45')
    expect(table).toHaveAttribute('tabindex', '0')
    expect(table).toHaveClass('overflow-x-auto')
    expect(get).toHaveBeenCalledWith(
      '/api/user/account-pool',
      expect.objectContaining({
        params: expect.objectContaining({ page: 1, page_size: 20 }),
      })
    )
  })
  test('supports 10/20/50 pagination and resets page after searching', async () => {
    const get = vi
      .spyOn(api, 'get')
      .mockResolvedValue({ data: { success: true, data: snapshot() } })
    const user = userEvent.setup()
    renderPool()
    await screen.findByText('AC-0123abcd')
    await user.click(screen.getByRole('button', { name: 'Go to next page' }))
    await waitFor(() =>
      expect(get).toHaveBeenLastCalledWith(
        '/api/user/account-pool',
        expect.objectContaining({
          params: expect.objectContaining({ page: 2 }),
        })
      )
    )
    await user.type(
      screen.getByRole('textbox', { name: 'Search accounts' }),
      'AC-01'
    )
    await waitFor(() =>
      expect(get).toHaveBeenLastCalledWith(
        '/api/user/account-pool',
        expect.objectContaining({
          params: expect.objectContaining({ page: 1, q: 'AC-01' }),
        })
      )
    )
    const size = screen.getByRole('combobox', { name: 'Rows per page' })
    expect(
      within(size)
        .getAllByRole('option')
        .map((option) => option.getAttribute('value'))
    ).toEqual(['10', '20', '50'])
    await user.selectOptions(size, '50')
    await waitFor(() =>
      expect(get).toHaveBeenLastCalledWith(
        '/api/user/account-pool',
        expect.objectContaining({
          params: expect.objectContaining({ page_size: 50 }),
        })
      )
    )
  })
  test('marks unconfigured and failed requests without showing simulated accounts', async () => {
    const get = vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: {
          ...snapshot(),
          configured: false,
          items: [],
          groups: [],
          updated_at: 0,
        },
      },
    })
    const user = userEvent.setup()
    renderPool()
    expect(
      await screen.findByText('Account pool is not configured')
    ).toBeVisible()
    expect(screen.queryByText('AC-0123abcd')).not.toBeInTheDocument()
    get.mockResolvedValue({ data: { success: false, message: 'Unavailable' } })
    await user.click(screen.getByRole('button', { name: 'Refresh' }))
    expect(
      await screen.findByText('Account pool data is unavailable')
    ).toBeVisible()
  })
  test('disables refresh while loading and responds to language changes', async () => {
    let resolve: (value: unknown) => void = () => undefined
    vi.spyOn(api, 'get').mockImplementation(
      () =>
        new Promise((done) => {
          resolve = done
        })
    )
    renderPool()
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeDisabled()
    resolve({ data: { success: true, data: snapshot() } })
    await screen.findByText('AC-0123abcd')
    await i18next.changeLanguage('zhCN')
    expect(await screen.findByRole('heading', { name: '账号池' })).toBeVisible()
    expect(screen.getByRole('textbox', { name: '搜索账号' })).toBeVisible()
    expect(screen.getByRole('button', { name: '刷新' })).toBeVisible()
    expect(screen.getByText(/^上次更新时间：/)).toBeVisible()
  })
})

test('disabled status hides the cached account list and makes no account request', async () => {
  const get = vi.spyOn(api, 'get')
  renderPool(false, snapshot())
  expect(screen.getByText('Account pool is disabled')).toBeVisible()
  expect(screen.queryByText('AC-0123abcd')).not.toBeInTheDocument()
  expect(get).not.toHaveBeenCalled()
})

test('an old cached enabled list is hidden until the server accepts a fresh request', async () => {
  let reject: (error: unknown) => void = () => undefined
  vi.spyOn(api, 'get').mockImplementation(
    () =>
      new Promise((_resolve, fail) => {
        reject = fail
      })
  )
  const { client } = renderPool(true, snapshot())
  expect(screen.queryByText('AC-0123abcd')).not.toBeInTheDocument()
  reject({
    response: {
      status: 403,
      data: {
        success: false,
        code: 'ACCOUNT_POOL_DISABLED',
        message: 'Account pool is disabled',
      },
    },
  })
  expect(await screen.findByText('Account pool is disabled')).toBeVisible()
  expect(screen.queryByText('AC-0123abcd')).not.toBeInTheDocument()
  await waitFor(() =>
    expect(
      client
        .getQueriesData({ queryKey: ['account-pool', 'snapshot'] })
        .every(([, data]) => data === undefined)
    ).toBe(true)
  )
  expect(client.getQueryData(['status'])).toMatchObject({
    account_pool_enabled: false,
  })
})

test('a failed refresh hides previously visible data instead of retaining a stale list', async () => {
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValue({ data: { success: true, data: snapshot() } })
  const user = userEvent.setup()
  renderPool()
  await screen.findByText('AC-0123abcd')
  get.mockRejectedValue(new Error('unavailable'))
  await user.click(screen.getByRole('button', { name: 'Refresh' }))
  expect(
    await screen.findByText('Account pool data is unavailable')
  ).toBeVisible()
  expect(screen.queryByText('AC-0123abcd')).not.toBeInTheDocument()
})

test('returning to a mounted page rechecks access and removes the list after another node disables it', async () => {
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValue({ data: { success: true, data: snapshot() } })
  renderPool()
  await screen.findByText('AC-0123abcd')
  get.mockRejectedValue({
    response: {
      status: 403,
      data: { success: false, code: 'ACCOUNT_POOL_DISABLED' },
    },
  })
  act(() => {
    focusManager.setFocused(false)
    focusManager.setFocused(true)
  })
  expect(await screen.findByText('Account pool is disabled')).toBeVisible()
  expect(screen.queryByText('AC-0123abcd')).not.toBeInTheDocument()
  focusManager.setFocused(undefined)
})
