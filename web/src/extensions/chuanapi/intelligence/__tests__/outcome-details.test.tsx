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
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import extensionEn from '@/extensions/chuanapi/i18n/locales/en.json'
import en from '@/i18n/locales/en.json'
import { api } from '@/lib/api'

import { IntelligenceDashboard } from '../index'
import type {
  FingerprintResult,
  TestHistory,
  TestRequest,
  TestRun,
  TestSelection,
} from '../types'

const incompleteReport: FingerprintResult = {
  status: 'unscorable',
  reason: 'One probe did not provide a complete sample.',
  top_candidates: [],
  sample_quality: [
    {
      challenge_id: 'probe-1',
      expected_count: 300,
      parsed_numbers: 300,
      minimum_numbers: 270,
      accepted: true,
      truncated: false,
      out_of_range_count: 0,
      reason: '',
    },
    {
      challenge_id: 'probe-2',
      expected_count: 300,
      parsed_numbers: 120,
      minimum_numbers: 270,
      accepted: false,
      truncated: true,
      out_of_range_count: 0,
      reason: 'The second sample was truncated.',
    },
    {
      challenge_id: 'probe-3',
      expected_count: 300,
      parsed_numbers: 300,
      minimum_numbers: 270,
      accepted: true,
      truncated: false,
      out_of_range_count: 0,
      reason: '',
    },
  ],
  used_outputs: 2,
  data_version: '2026-11-07',
  data_sha256: 'reference-hash',
  sdk_commit: 'sdk-pinned',
  probability_note: '',
  probability_status: 'insufficient_samples',
  strict: true,
}

function availableHistory(model = 'first-model'): TestHistory {
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
      timeout_seconds: 150,
      runs_per_hour: 3,
      concurrency_per_user: 1,
    },
    site_results: [],
    items: [],
    eligible_models_by_group: { default: [model] },
    eligible_tokens_by_group: {
      default: [
        {
          id: 7,
          name: 'Matching key',
          group: 'default',
          model_limits_enabled: false,
          model_limits: [],
          eligible_models: [model],
        },
      ],
    },
  }
}

function terminalRun(overrides: Partial<TestRun> = {}): TestRun {
  return {
    id: 'completed-test',
    model: 'first-model',
    group: 'default',
    benchmark_id: 'local-fingerprint-v1',
    source: 'user',
    status: 'failed',
    assessment: 'unscorable',
    created_at: 1800000000,
    completed_at: 1800000001,
    max_output_tokens: 0,
    estimated_quota: null,
    request_id: 'completed-test',
    error_code: 'incomplete_response',
    cancel_requested: false,
    fingerprint_result: incompleteReport,
    ...overrides,
  }
}

let client: QueryClient | undefined

function renderDashboard(outcome = terminalRun()) {
  const server = { history: availableHistory(), outcome, loseResponse: false }
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (String(url).endsWith('/site')) {
      return {
        data: {
          success: true,
          data: {
            enabled: server.history.enabled,
            items: [],
            user_items: [],
            targets: server.history.enabled
              ? Object.entries(
                  server.history.eligible_models_by_group ?? {}
                ).flatMap(([group, models]) =>
                  models.map((model) => ({
                    id: `${group}/${model}`,
                    group,
                    model,
                    enabled: true,
                    period_secs: 3600,
                  }))
                )
              : [],
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
            models_by_group: server.history.eligible_models_by_group,
          },
        },
      }
    }
    if (url === '/api/user/model-tests') {
      return { data: { success: true, data: server.history } }
    }
    throw new Error(`Unexpected read: ${url}`)
  })
  const post = vi.spyOn(api, 'post').mockImplementation(async (url, body) => {
    if (String(url).endsWith('/estimate')) {
      return {
        data: {
          success: true,
          data: {
            ...(body as TestSelection),
            estimated_quota: null,
            quota_per_unit: 500000,
            estimate_is_cap: false,
            timeout_seconds: 150,
            benchmark_id: 'local-fingerprint-v1',
            confirmation_token: 'confirmed-test',
            expires_at: Math.floor(Date.now() / 1000) + 300,
          },
        },
      }
    }
    if (String(url).endsWith('/runs')) {
      const request = body as TestRequest
      const saved = {
        ...server.outcome,
        id: request.request_id,
        request_id: request.request_id,
        model: request.model,
        group: request.group,
      }
      server.history = { ...server.history, items: [saved] }
      if (server.loseResponse) throw new Error('Response lost after save')
      return { data: { success: true, data: saved } }
    }
    throw new Error(`Unexpected write: ${url}`)
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
  return { server, get, post }
}

async function runTest(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByRole('button', { name: 'Self-test' }))
  await user.selectOptions(
    await screen.findByLabelText('Select an existing API key'),
    '7'
  )
  await user.click(
    screen.getByRole('button', { name: 'Review cost and start' })
  )
  await user.click(
    await screen.findByRole('button', { name: 'Accept charges and run once' })
  )
}

beforeEach(async () => {
  i18next.addResourceBundle('en', 'translation', en.translation, true, true)
  i18next.addResourceBundle(
    'en',
    'translation',
    extensionEn.translation,
    true,
    true
  )
  await i18next.changeLanguage('en')
})

afterEach(async () => {
  cleanup()
  await client?.cancelQueries()
  client?.clear()
})

test.each([
  { status: 'failed' as const, error_code: 'incomplete_response' },
  { status: 'unknown' as const, error_code: 'upstream_outcome_unknown' },
])(
  'the $status outcome opens all three samples before it appears in public history',
  async (outcome) => {
    const { get, post } = renderDashboard(terminalRun(outcome))
    const user = userEvent.setup()
    await runTest(user)
    await user.click(
      await screen.findByRole('button', { name: 'View details' })
    )

    const dialog = await screen.findByRole('dialog', { name: 'first-model' })
    expect(within(dialog).getByText('Accepted samples: 2 / 3')).toBeVisible()
    expect(within(dialog).getByText('probe-1')).toBeVisible()
    expect(within(dialog).getByText('probe-2')).toBeVisible()
    expect(within(dialog).getByText('probe-3')).toBeVisible()
    expect(
      within(dialog).getByText('The second sample was truncated.')
    ).toBeVisible()
    expect(
      within(dialog).getByText('Unknown / insufficient evidence')
    ).toBeVisible()
    expect(get.mock.calls.some(([url]) => String(url).includes('/runs/'))).toBe(
      false
    )
    expect(
      post.mock.calls.filter(([url]) => String(url).endsWith('/runs'))
    ).toHaveLength(1)
  }
)

test('a recovered analysis failure opens its reason without inventing a report or replaying the test', async () => {
  const { server, post } = renderDashboard(
    terminalRun({
      error_code: 'fingerprint_analysis_failed',
      fingerprint_result: null,
    })
  )
  server.loseResponse = true
  const user = userEvent.setup()
  await runTest(user)
  await user.click(await screen.findByRole('button', { name: 'View details' }))

  const dialog = await screen.findByRole('dialog', { name: 'first-model' })
  expect(
    within(dialog).getByText(
      en.translation[
        'Fingerprint analysis failed. This is not a model mismatch.'
      ]
    )
  ).toBeVisible()
  expect(
    within(dialog).getByText(en.translation['fingerprint.noReport'])
  ).toBeVisible()
  expect(
    within(dialog).queryByText(/Accepted samples:/)
  ).not.toBeInTheDocument()
  expect(
    post.mock.calls.filter(([url]) => String(url).endsWith('/runs'))
  ).toHaveLength(1)
})

test('disabling tests keeps the completed outcome readable without offering another paid run', async () => {
  const { server, post } = renderDashboard()
  const user = userEvent.setup()
  await runTest(user)
  await screen.findByRole('button', { name: 'View details' })
  server.history = { ...server.history, enabled: false }
  await user.click(
    screen.getByRole('button', {
      name: extensionEn.translation['monitor.refresh'],
    })
  )
  await screen.findByText(en.translation['fingerprint.testsDisabled'])
  expect(
    screen.queryByRole('button', { name: 'Self-test' })
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'View details' }))

  const dialog = await screen.findByRole('dialog', { name: 'first-model' })
  expect(within(dialog).getByText('Accepted samples: 2 / 3')).toBeVisible()
  expect(
    post.mock.calls.filter(([url]) => String(url).endsWith('/runs'))
  ).toHaveLength(1)
})

test('a subsequent target opens its own outcome instead of the previous target report', async () => {
  const { server } = renderDashboard()
  const user = userEvent.setup()
  await runTest(user)
  await user.click(await screen.findByRole('button', { name: 'View details' }))
  expect(
    await screen.findByRole('dialog', { name: 'first-model' })
  ).toBeVisible()
  await user.keyboard('{Escape}')
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )

  server.history = availableHistory('second-model')
  server.outcome = terminalRun({
    status: 'failed',
    error_code: 'fingerprint_analysis_failed',
    fingerprint_result: null,
  })
  await user.click(
    screen.getByRole('button', {
      name: extensionEn.translation['monitor.refresh'],
    })
  )
  await screen.findByRole('heading', { name: 'second-model' })
  await runTest(user)
  await user.click(await screen.findByRole('button', { name: 'View details' }))

  const dialog = await screen.findByRole('dialog', { name: 'second-model' })
  expect(
    within(dialog).getByText(en.translation['fingerprint.noReport'])
  ).toBeVisible()
  expect(within(dialog).queryByText('probe-1')).not.toBeInTheDocument()
  expect(within(dialog).queryByText('first-model')).not.toBeInTheDocument()
})
