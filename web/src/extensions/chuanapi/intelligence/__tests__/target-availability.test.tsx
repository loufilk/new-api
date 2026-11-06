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
  act,
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import { api } from '@/lib/api'

import { IntelligenceDashboard } from '../index'
import type { TestEstimate, TestHistory, TestSelection } from '../types'

let client: QueryClient | undefined

beforeEach(async () => {
  i18next.addResourceBundle('en', 'translation', en.translation, true, true)
  await i18next.changeLanguage('en')
})

afterEach(async () => {
  cleanup()
  await client?.cancelQueries()
  client?.clear()
})

function availableHistory(): TestHistory {
  return {
    enabled: true,
    benchmark: {
      id: 'local-fingerprint-v1',
      name: 'Fingerprint',
      prompt: 'Public fingerprint samples',
      license: 'CC0-1.0',
      public_prompt: true,
      assessment: 'statistical-not-proof',
    },
    limits: {
      max_output_tokens: 0,
      default_output_tokens: 0,
      timeout_seconds: 90,
      runs_per_hour: 3,
      concurrency_per_user: 1,
    },
    site_results: [],
    items: [],
    eligible_models_by_group: { default: ['test-model'] },
    eligible_tokens_by_group: {
      default: [
        {
          id: 7,
          name: 'My matching key',
          group: 'default',
          model_limits_enabled: false,
          model_limits: [],
          eligible_models: ['test-model'],
        },
      ],
    },
  }
}

function estimateFor(selection: TestSelection): TestEstimate {
  return {
    ...selection,
    estimated_quota: null,
    quota_per_unit: 500000,
    estimate_is_cap: false,
    timeout_seconds: 90,
    benchmark_id: 'local-fingerprint-v1',
    confirmation_token: 'target-quote',
    expires_at: Math.floor(Date.now() / 1000) + 300,
  }
}

function renderDashboard() {
  const server = { history: availableHistory() }
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (String(url).endsWith('/site')) {
      return {
        data: {
          success: true,
          data: {
            enabled: true,
            items: [],
            targets: Object.entries(
              server.history.eligible_models_by_group ?? {}
            ).flatMap(([group, models]) =>
              models.map((model) => ({
                id: `${group}/${model}`,
                group,
                model,
                enabled: true,
                period_secs: 3600,
              }))
            ),
          },
        },
      }
    }
    if (String(url).endsWith('/bootstrap')) {
      return {
        data: {
          success: true,
          data: {
            groups: [{ name: 'default', desc: '', ratio: 1 }],
            models_by_group: { default: ['test-model'] },
          },
        },
      }
    }
    return { data: { success: true, data: server.history } }
  })
  client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  })
  render(
    <QueryClientProvider client={client}>
      <IntelligenceDashboard />
    </QueryClientProvider>
  )
  return server
}

async function beginEstimate(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByRole('button', { name: 'Self-test' }))
  await user.selectOptions(
    await screen.findByLabelText('Select an existing API key'),
    '7'
  )
  await user.click(
    screen.getByRole('button', { name: 'Review cost and start' })
  )
}

async function refreshAvailability() {
  await act(async () => {
    await client?.invalidateQueries({ queryKey: ['model-tests'] })
  })
}

test('revoking a target closes its quote and reenabling it cannot reuse the old confirmation', async () => {
  const server = renderDashboard()
  const post = vi.spyOn(api, 'post').mockImplementation(async (_url, body) => ({
    data: { success: true, data: estimateFor(body as TestSelection) },
  }))
  const user = userEvent.setup()
  await beginEstimate(user)
  expect(await screen.findByRole('alertdialog')).toBeVisible()

  server.history = {
    ...server.history,
    eligible_models_by_group: {},
    eligible_tokens_by_group: {},
  }
  await refreshAvailability()
  await waitFor(() => {
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  })
  expect(
    screen.queryByRole('button', { name: 'Self-test' })
  ).not.toBeInTheDocument()

  server.history = availableHistory()
  await refreshAvailability()
  expect(await screen.findByRole('button', { name: 'Self-test' })).toBeEnabled()
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  expect(post).toHaveBeenCalledTimes(1)
})

test('an estimate from before target revocation cannot reopen a quote after the target is reenabled', async () => {
  const server = renderDashboard()
  let resolveEstimate: () => void = () => {
    throw new Error('Estimate was not requested')
  }
  const post = vi.spyOn(api, 'post').mockImplementation(
    (_url, body) =>
      new Promise((resolve) => {
        resolveEstimate = () =>
          resolve({
            data: { success: true, data: estimateFor(body as TestSelection) },
          })
      })
  )
  const user = userEvent.setup()
  await beginEstimate(user)
  await waitFor(() => expect(post).toHaveBeenCalledTimes(1))

  server.history = { ...server.history, eligible_models_by_group: {} }
  await refreshAvailability()
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )

  server.history = availableHistory()
  await refreshAvailability()
  await act(async () => resolveEstimate())
  await waitFor(() => {
    expect(screen.getByRole('button', { name: 'Self-test' })).toBeEnabled()
  })
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  expect(post).toHaveBeenCalledTimes(1)
})

test('revoking an unrelated target keeps a still eligible model quote available', async () => {
  const server = renderDashboard()
  server.history.eligible_models_by_group = {
    default: ['test-model'],
    another: ['other-model'],
  }
  vi.spyOn(api, 'post').mockImplementation(async (_url, body) => ({
    data: { success: true, data: estimateFor(body as TestSelection) },
  }))
  const user = userEvent.setup()
  const group = await screen.findByRole('region', { name: 'default' })
  await user.click(within(group).getByRole('button', { name: 'Self-test' }))
  await user.selectOptions(
    await screen.findByLabelText('Select an existing API key'),
    '7'
  )
  await user.click(
    screen.getByRole('button', { name: 'Review cost and start' })
  )
  expect(await screen.findByRole('alertdialog')).toBeVisible()

  server.history = availableHistory()
  await refreshAvailability()
  const quote = screen.getByRole('alertdialog')
  expect(
    within(quote).getByRole('button', {
      name: 'Accept charges and run once',
    })
  ).toBeEnabled()
})
