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
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { act, render, screen, within, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { getSiteTests } from '../api'
import { IntelligenceDashboard } from '../index'

const shared = {
  id: 'public-run',
  source: 'user',
  model: 'shared-model',
  group: 'Public group',
  benchmark_id: 'local-fingerprint-v1',
  status: 'completed',
  assessment: 'statistical',
  created_at: 1800000000,
  completed_at: 1800000001,
  fingerprint_result: {
    status: 'scored',
    reason: '',
    top_candidates: [
      {
        model: 'reference',
        display_name: 'Shared reference',
        family: 'reference',
        family_name: 'Reference',
        score: 2,
        probability: 0.75,
      },
    ],
    sample_quality: [],
    data_version: 'test',
    data_sha256: 'test',
    sdk_commit: 'test',
    probability_note: '',
    probability_status: 'available',
    used_outputs: 3,
    strict: true,
  },
}

beforeEach(async () => {
  i18next.addResourceBundle('en', 'translation', en.translation, true, true)
  await i18next.changeLanguage('en')
})

test('public manual results discard unexpected personal fields and never gain private detail access', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        items: [],
        targets: [],
        user_items: [
          {
            ...shared,
            user_id: 19,
            token_id: 8,
            token_name: 'private-key-name',
            request_id: 'private-request',
            error_code: 'raw upstream error',
            artwork_html: 'private output',
            prompt: 'private prompt',
          },
        ],
      },
    },
  })
  const data = await getSiteTests()
  expect(data.user_items).toHaveLength(1)
  expect(data.user_items?.[0]).toMatchObject({
    id: 'public-run',
    visibility: 'public',
    request_id: '',
    error_code: '',
  })
  expect(data.user_items?.[0]).not.toHaveProperty('token_id')
  expect(data.user_items?.[0]).not.toHaveProperty('user_id')
  expect(data.user_items?.[0]).not.toHaveProperty('token_name')
  expect(data.user_items?.[0]).not.toHaveProperty('prompt')
  expect(data.user_items?.[0]).not.toHaveProperty('artwork_html')
})

test('another user’s result appears by group and model and opens without a personal detail request', async () => {
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (String(url).endsWith('/site')) {
      return {
        data: {
          success: true,
          data: {
            enabled: false,
            items: [],
            targets: [],
            user_items: [shared],
          },
        },
      }
    }
    return {
      data: {
        success: true,
        data: {
          enabled: false,
          items: [],
          site_results: [],
          benchmark: {},
          limits: {},
        },
      },
    }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={client}>
      <IntelligenceDashboard />
    </QueryClientProvider>
  )
  await screen.findByText(en.translation['fingerprint.testsDisabled'])
  expect(await screen.findByText('No enabled test targets')).toBeVisible()
  expect(
    screen.queryByRole('heading', { name: 'shared-model' })
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('tab', { name: 'Historical results' }))
  expect(
    await screen.findByRole('heading', { name: 'Public group' })
  ).toBeVisible()
  expect(screen.getByRole('heading', { name: 'shared-model' })).toBeVisible()
  const timeline = screen.getByRole('list', { name: /User tests/ })
  await user.click(within(timeline).getByRole('button'))
  expect(await screen.findByRole('dialog')).toHaveTextContent(
    'Shared reference'
  )
  expect(get.mock.calls.some(([url]) => String(url).includes('/runs/'))).toBe(
    false
  )
  await user.keyboard('{Escape}')
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  await user.click(within(timeline).getByRole('button'))
  expect(await screen.findByRole('dialog')).toHaveTextContent(
    'Shared reference'
  )
})

afterEach(() => useAuthStore.getState().auth.reset())

test('admins see shared results without a configuration shortcut or extra history and sharing sections', async () => {
  useAuthStore.getState().auth.setUser({ id: 7, username: 'admin', role: 10 })
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (String(url).endsWith('/site')) {
      return {
        data: {
          success: true,
          data: {
            enabled: false,
            items: [],
            targets: [],
            user_items: [shared],
          },
        },
      }
    }
    return {
      data: {
        success: true,
        data: {
          enabled: false,
          items: [
            {
              ...shared,
              id: 'private-old',
              model: 'private-only-model',
              group: 'Personal history',
            },
          ],
          effective_points: [
            {
              ...shared,
              id: 'private-effective',
              model: 'private-only-model',
              group: 'Personal history',
            },
          ],
          site_results: [],
          benchmark: {},
          limits: {},
        },
      },
    }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  const root = createRootRoute({ component: () => <IntelligenceDashboard /> })
  const router = createRouter({
    routeTree: root,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  await screen.findByText(en.translation['fingerprint.testsDisabled'])
  expect(
    screen.queryByRole('heading', { name: 'shared-model' })
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('tab', { name: 'Historical results' }))
  expect(
    await screen.findByRole('heading', { name: 'shared-model' })
  ).toBeVisible()
  expect(
    screen.queryByRole('link', { name: 'Configure fingerprint tests' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('heading', { name: 'Personal history' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('heading', { name: 'private-only-model' })
  ).not.toBeInTheDocument()
  expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
  expect(
    screen.queryByRole('heading', { name: 'My latest 60 tests' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByText(en.translation['fingerprint.sharedResultsHelp'])
  ).not.toBeInTheDocument()
  expect(screen.queryByRole('note')).not.toBeInTheDocument()
})

test('current targets exclude disabled, unconfigured and historical models while keyboard navigation keeps history accessible', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (String(url).endsWith('/site')) {
      return {
        data: {
          success: true,
          data: {
            enabled: true,
            targets: [
              {
                id: 'active',
                group: 'Current group',
                model: 'enabled-model',
                enabled: true,
                period_secs: 3600,
              },
              {
                id: 'disabled',
                group: 'Current group',
                model: 'disabled-model',
                enabled: false,
                period_secs: 3600,
              },
            ],
            items: [],
            user_items: [shared],
          },
        },
      }
    }
    if (String(url).endsWith('/bootstrap')) {
      return {
        data: { success: true, data: { groups: [], models_by_group: {} } },
      }
    }
    return {
      data: {
        success: true,
        data: {
          enabled: true,
          items: [],
          site_results: [],
          benchmark: {},
          limits: {},
          eligible_models_by_group: {
            'Current group': [
              'enabled-model',
              'callable-only',
              'disabled-model',
            ],
          },
        },
      },
    }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={client}>
      <IntelligenceDashboard />
    </QueryClientProvider>
  )
  expect(
    await screen.findByRole('heading', { name: 'enabled-model' })
  ).toBeVisible()
  for (const name of ['disabled-model', 'callable-only', 'shared-model']) {
    expect(screen.queryByRole('heading', { name })).not.toBeInTheDocument()
  }
  expect(screen.getAllByRole('button', { name: 'Self-test' })).toHaveLength(1)
  const current = screen.getByRole('tab', { name: 'Current targets' })
  expect(screen.getByRole('tablist', { name: 'Model test views' })).toHaveClass(
    'max-w-full',
    'flex-wrap'
  )
  expect(current).toHaveAttribute('aria-selected', 'true')
  current.focus()
  await user.keyboard('{ArrowRight}')
  expect(screen.getByRole('tab', { name: 'Historical results' })).toHaveFocus()
  await user.keyboard('{Enter}')
  expect(
    screen.getByRole('tab', { name: 'Historical results' })
  ).toHaveAttribute('aria-selected', 'true')
  expect(
    await screen.findByRole('heading', { name: 'shared-model' })
  ).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Self-test' })
  ).not.toBeInTheDocument()
})

test('a cached target list stays hidden until a fresh site response confirms the current targets', async () => {
  let finishSite: () => void = () => undefined
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (String(url).endsWith('/site')) {
      return new Promise((resolve) => {
        finishSite = () =>
          resolve({
            data: {
              success: true,
              data: {
                enabled: true,
                targets: [
                  {
                    id: 'fresh',
                    group: 'Public',
                    model: 'fresh-model',
                    enabled: true,
                    period_secs: 3600,
                  },
                ],
                items: [],
              },
            },
          })
      })
    }
    return {
      data: {
        success: true,
        data: {
          enabled: false,
          items: [],
          site_results: [],
          benchmark: {},
          limits: {},
        },
      },
    }
  })
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: Infinity },
    },
  })
  client.setQueryData(['model-tests', 'site'], {
    enabled: true,
    targets: [
      {
        id: 'old',
        group: 'Public',
        model: 'stale-model',
        enabled: true,
        period_secs: 3600,
      },
    ],
    items: [],
  })
  render(
    <QueryClientProvider client={client}>
      <IntelligenceDashboard />
    </QueryClientProvider>
  )
  expect(
    screen.queryByRole('heading', { name: 'stale-model' })
  ).not.toBeInTheDocument()
  await act(async () => finishSite())
  expect(
    await screen.findByRole('heading', { name: 'fresh-model' })
  ).toBeVisible()
  expect(
    screen.queryByRole('heading', { name: 'stale-model' })
  ).not.toBeInTheDocument()
})
