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
import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import { api } from '@/lib/api'

import { IntelligenceDashboard } from '../index'
import type { TestHistory, TestRun, TestSelection, TestRequest } from '../types'

function history(items: TestRun[] = []): TestHistory {
  return {
    enabled: true,
    benchmark: {
      id: 'pelican-static-v1',
      name: 'Pelican',
      prompt: 'Create a pelican riding a bicycle using static HTML and SVG.',
      license: 'CC0-1.0',
      public_prompt: true,
      assessment: 'structural-only',
    },
    limits: {
      max_output_tokens: 4096,
      default_output_tokens: 2048,
      timeout_seconds: 90,
      runs_per_hour: 3,
      concurrency_per_user: 1,
    },
    site_results: [],
    items,
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
function run(overrides: Partial<TestRun> = {}): TestRun {
  return {
    id: 'test-run',
    model: 'test-model',
    group: 'default',
    benchmark_id: 'pelican-static-v1',
    status: 'completed',
    assessment: 'renderable',
    created_at: 1800000000,
    completed_at: 1800000001,
    max_output_tokens: 2048,
    estimated_quota: null,
    request_id: 'relay-request',
    error_code: '',
    cancel_requested: false,
    ...overrides,
  }
}
function renderWithQuery(content: React.ReactNode) {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  })
  return {
    client,
    ...render(
      <QueryClientProvider client={client}>{content}</QueryClientProvider>
    ),
  }
}
function mockReads(
  items: TestRun[] = [],
  availability: { enabled?: unknown } = { enabled: true }
) {
  return vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (String(url).endsWith('/site')) {
      return { data: { success: true, data: { items: [], targets: [] } } }
    }
    if (String(url).endsWith('/bootstrap')) {
      return {
        data: {
          success: true,
          data: {
            groups: [{ name: 'default', desc: '', ratio: 1.5 }],
            models_by_group: { default: ['test-model', 'not-eligible'] },
          },
        },
      }
    }
    if (String(url).includes('/runs/')) {
      return {
        data: {
          success: true,
          data: {
            ...run(),
            artwork_html: '<svg><circle r="5" /></svg>',
            prompt: 'Prompt',
          },
        },
      }
    }
    return {
      data: { success: true, data: { ...history(items), ...availability } },
    }
  })
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

beforeEach(async () => {
  i18next.addResourceBundle('en', 'translation', en.translation, true, true)
  await i18next.changeLanguage('en')
})
describe('explicit model test workflow', () => {
  test('never starts on load, filters ineligible models, and requires a cost confirmation before one paid request', async () => {
    mockReads()
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
              timeout_seconds: 90,
              benchmark_id: 'pelican-static-v1',
              confirmation_token: 'confirmation',
              expires_at: Math.floor(Date.now() / 1000) + 300,
            },
          },
        }
      }
      return { data: { success: true, data: run() } }
    })
    const user = userEvent.setup()
    renderWithQuery(<IntelligenceDashboard />)
    expect(
      await screen.findByRole('heading', { name: 'test-model' })
    ).toBeVisible()
    expect(
      screen.queryByRole('heading', { name: 'not-eligible' })
    ).not.toBeInTheDocument()
    expect(post).not.toHaveBeenCalled()
    await beginEstimate(user)
    const dialog = await screen.findByRole('alertdialog')
    expect(within(dialog).getByText(/not a hard monetary cap/)).toBeVisible()
    expect(
      within(dialog).getByText(
        /Unavailable — actual configured billing applies/
      )
    ).toBeVisible()
    expect(post).toHaveBeenCalledTimes(1)
    await user.click(
      within(dialog).getByRole('button', {
        name: 'Accept charges and run once',
      })
    )
    await waitFor(() => expect(post).toHaveBeenCalledTimes(2))
    expect(post.mock.calls[1][0]).toBe('/api/user/model-tests/runs')
    expect(post.mock.calls[1][1]).toMatchObject({
      model: 'test-model',
      group: 'default',
      max_output_tokens: 0,
      token_id: 7,
      confirmation_token: 'confirmation',
      confirmed: true,
    })
    expect((post.mock.calls[1][1] as TestRequest).request_id).toMatch(
      /^[a-f0-9-]{36}$/
    )
  })
  test.each([false, true])(
    'preflight rejection only clears a request without an earlier unknown outcome (unknown: %s)',
    async (lostFirst) => {
      mockReads()
      let attempts = 0
      vi.spyOn(api, 'post').mockImplementation(async (url, body) => {
        if (String(url).endsWith('/estimate')) {
          return {
            data: {
              success: true,
              data: {
                ...(body as TestSelection),
                estimated_quota: null,
                quota_per_unit: 500000,
                estimate_is_cap: false,
                timeout_seconds: 90,
                benchmark_id: 'pelican-static-v1',
                confirmation_token: 'confirmation',
                expires_at: Math.floor(Date.now() / 1000) + 300,
              },
            },
          }
        }
        attempts++
        if (lostFirst && attempts === 1) throw new Error('Connection lost')
        throw {
          response: {
            status: 409,
            data: { code: 'confirmation_price_changed', success: false },
          },
        }
      })
      const user = userEvent.setup()
      renderWithQuery(<IntelligenceDashboard />)
      await beginEstimate(user)
      await user.click(
        await screen.findByRole('button', {
          name: 'Accept charges and run once',
        })
      )
      if (lostFirst) {
        await user.click(
          await screen.findByRole('button', { name: 'Retry the same request' })
        )
        await waitFor(() =>
          expect(
            screen.getByRole('button', { name: 'Retry the same request' })
          ).toBeEnabled()
        )
        expect(screen.getByRole('button', { name: 'Self-test' })).toBeDisabled()
      } else {
        await waitFor(() =>
          expect(
            screen.getByRole('button', { name: 'Self-test' })
          ).toBeEnabled()
        )
        expect(
          screen.queryByRole('button', { name: 'Retry the same request' })
        ).not.toBeInTheDocument()
      }
    }
  )
  test('sends a durable cancellation for a running test and keeps new runs disabled', async () => {
    mockReads([run({ status: 'running', assessment: 'unassessed' })])
    const post = vi.spyOn(api, 'post').mockResolvedValue({
      data: {
        success: true,
        data: run({ cancel_requested: true, status: 'running' }),
      },
    })
    const user = userEvent.setup()
    renderWithQuery(<IntelligenceDashboard />)
    expect(
      await screen.findByRole('button', { name: 'Self-test' })
    ).toBeDisabled()
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(post).toHaveBeenCalledWith(
      '/api/user/model-tests/runs/test-run/cancel'
    )
  })
  test('retries a lost response with the same idempotent request identity', async () => {
    mockReads()
    let attempts = 0
    const post = vi.spyOn(api, 'post').mockImplementation(async (url, body) => {
      if (String(url).endsWith('/estimate')) {
        return {
          data: {
            success: true,
            data: {
              ...(body as TestSelection),
              estimated_quota: 100,
              quota_per_unit: 500000,
              estimate_is_cap: false,
              timeout_seconds: 90,
              benchmark_id: 'pelican-static-v1',
              confirmation_token: 'confirmation',
              expires_at: Math.floor(Date.now() / 1000) + 300,
            },
          },
        }
      }
      attempts++
      if (attempts === 1) throw new Error('Connection lost')
      return { data: { success: true, data: run() } }
    })
    const user = userEvent.setup()
    renderWithQuery(<IntelligenceDashboard />)
    await beginEstimate(user)
    await user.click(
      await screen.findByRole('button', { name: 'Accept charges and run once' })
    )
    await user.click(
      await screen.findByRole('button', { name: 'Retry the same request' })
    )
    await waitFor(() => expect(post).toHaveBeenCalledTimes(3))
    expect(post.mock.calls[2][1]).toEqual(post.mock.calls[1][1])
  })
  test('unlocks new tests after a lost response is reconciled to a saved terminal result', async () => {
    let saved: TestRun | null = null
    const get = mockReads()
    get.mockImplementation(async (url) => {
      if (String(url).endsWith('/site')) {
        return { data: { success: true, data: { items: [], targets: [] } } }
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
      return { data: { success: true, data: history(saved ? [saved] : []) } }
    })
    vi.spyOn(api, 'post').mockImplementation(async (url, body) => {
      if (String(url).endsWith('/estimate')) {
        return {
          data: {
            success: true,
            data: {
              ...(body as TestSelection),
              estimated_quota: null,
              quota_per_unit: 500000,
              estimate_is_cap: false,
              timeout_seconds: 90,
              benchmark_id: 'pelican-static-v1',
              confirmation_token: 'confirmation',
              expires_at: Math.floor(Date.now() / 1000) + 300,
            },
          },
        }
      }
      saved = run({
        id: (body as TestRequest).request_id,
        status: 'failed',
        assessment: 'unassessed',
      })
      throw new Error('Response lost after save')
    })
    const user = userEvent.setup()
    renderWithQuery(<IntelligenceDashboard />)
    await beginEstimate(user)
    await user.click(
      await screen.findByRole('button', { name: 'Accept charges and run once' })
    )
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Self-test' })).toBeEnabled()
    )
    expect(
      screen.queryByRole('button', { name: 'Retry the same request' })
    ).not.toBeInTheDocument()
    expect(screen.getByText('Failed')).toBeVisible()
  })
  test('reports request failures without treating errors as valid model results', async () => {
    vi.spyOn(api, 'get').mockRejectedValue(new Error('Offline'))
    renderWithQuery(<IntelligenceDashboard />)
    expect(await screen.findByRole('button', { name: 'Retry' })).toBeVisible()
    expect(
      screen.queryByRole('button', { name: 'Self-test' })
    ).not.toBeInTheDocument()
  })
})

describe('model test availability', () => {
  test.each([undefined, false, 'true', 1])(
    'does not offer new tests unless history enabled is boolean true (%s)',
    async (enabled) => {
      const get = mockReads(
        [run({ status: 'running', assessment: 'unassessed' })],
        { enabled }
      )
      const post = vi.spyOn(api, 'post').mockResolvedValue({
        data: { success: true, data: run({ cancel_requested: true }) },
      })
      const user = userEvent.setup()
      renderWithQuery(<IntelligenceDashboard />)
      expect(
        await screen.findByText(en.translation['fingerprint.testsDisabled'])
      ).toBeVisible()
      expect(
        screen.queryByRole('button', { name: 'Self-test' })
      ).not.toBeInTheDocument()
      expect(
        screen.queryByRole('heading', { name: 'My latest 60 tests' })
      ).not.toBeInTheDocument()
      await user.click(screen.getByRole('button', { name: 'Cancel' }))
      expect(post).toHaveBeenCalledExactlyOnceWith(
        '/api/user/model-tests/runs/test-run/cancel'
      )
      expect(
        get.mock.calls.some(([url]) => String(url).endsWith('/bootstrap'))
      ).toBe(false)
    }
  )

  test('closing the feature dismisses an existing quote and reopening cannot reuse it', async () => {
    mockReads()
    const post = vi
      .spyOn(api, 'post')
      .mockImplementation(async (_url, body) => ({
        data: {
          success: true,
          data: {
            ...(body as TestSelection),
            estimated_quota: 100,
            quota_per_unit: 500000,
            estimate_is_cap: false,
            timeout_seconds: 90,
            benchmark_id: 'pelican-static-v1',
            confirmation_token: 'old-quote',
            expires_at: Math.floor(Date.now() / 1000) + 300,
          },
        },
      }))
    const user = userEvent.setup()
    const { client } = renderWithQuery(<IntelligenceDashboard />)
    await beginEstimate(user)
    expect(await screen.findByRole('alertdialog')).toBeVisible()
    client.setQueryData(['model-tests', 'history'], {
      ...history(),
      enabled: false,
    })
    await waitFor(() =>
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    )
    client.setQueryData(['model-tests', 'history'], history())
    expect(
      await screen.findByRole('button', { name: 'Self-test' })
    ).toBeEnabled()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(post).toHaveBeenCalledTimes(1)
  })

  test('failed availability refresh closes a quote and never enables stale cached new-test controls', async () => {
    const get = mockReads()
    vi.spyOn(api, 'post').mockImplementation(async (_url, body) => ({
      data: {
        success: true,
        data: {
          ...(body as TestSelection),
          estimated_quota: 100,
          quota_per_unit: 500000,
          estimate_is_cap: false,
          timeout_seconds: 90,
          benchmark_id: 'pelican-static-v1',
          confirmation_token: 'old-quote',
          expires_at: Math.floor(Date.now() / 1000) + 300,
        },
      },
    }))
    const user = userEvent.setup()
    const { client } = renderWithQuery(<IntelligenceDashboard />)
    await beginEstimate(user)
    expect(await screen.findByRole('alertdialog')).toBeVisible()
    get.mockRejectedValue(new Error('offline'))
    await client.invalidateQueries({ queryKey: ['model-tests', 'history'] })
    await waitFor(() =>
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    )
    expect(
      screen.queryByRole('button', { name: 'Self-test' })
    ).not.toBeInTheDocument()
  })
})

test('an estimate response from before a disable-enable cycle cannot reopen the old confirmation', async () => {
  mockReads()
  let resolve: (value: unknown) => void = () => undefined
  const post = vi.spyOn(api, 'post').mockImplementation(
    (_url, body) =>
      new Promise((done) => {
        resolve = () =>
          done({
            data: {
              success: true,
              data: {
                ...(body as TestSelection),
                estimated_quota: 100,
                quota_per_unit: 500000,
                estimate_is_cap: false,
                timeout_seconds: 90,
                benchmark_id: 'pelican-static-v1',
                confirmation_token: 'late-quote',
                expires_at: Math.floor(Date.now() / 1000) + 300,
              },
            },
          })
      })
  )
  const user = userEvent.setup()
  const { client } = renderWithQuery(<IntelligenceDashboard />)
  await beginEstimate(user)
  await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
  act(() =>
    client.setQueryData(['model-tests', 'history'], {
      ...history(),
      enabled: false,
    })
  )
  expect(
    await screen.findByText(en.translation['fingerprint.testsDisabled'])
  ).toBeVisible()
  act(() => client.setQueryData(['model-tests', 'history'], history()))
  await screen.findAllByRole('button', { name: 'Estimating…' })
  await act(async () => resolve(undefined))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Self-test' })).toBeEnabled()
  )
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  expect(post).toHaveBeenCalledTimes(1)
})

test('disabled unknown-outcome requests retain cancellation without offering a paid retry', async () => {
  mockReads()
  const post = vi.spyOn(api, 'post').mockImplementation(async (url, body) => {
    if (String(url).endsWith('/estimate')) {
      return {
        data: {
          success: true,
          data: {
            ...(body as TestSelection),
            estimated_quota: 100,
            quota_per_unit: 500000,
            estimate_is_cap: false,
            timeout_seconds: 90,
            benchmark_id: 'pelican-static-v1',
            confirmation_token: 'quote',
            expires_at: Math.floor(Date.now() / 1000) + 300,
          },
        },
      }
    }
    if (String(url).endsWith('/cancel')) {
      return { data: { success: true, data: run({ cancel_requested: true }) } }
    }
    throw new Error('Unknown upstream outcome')
  })
  const user = userEvent.setup()
  const { client } = renderWithQuery(<IntelligenceDashboard />)
  await beginEstimate(user)
  await user.click(
    await screen.findByRole('button', { name: 'Accept charges and run once' })
  )
  await screen.findByRole('button', { name: 'Retry the same request' })
  const request = post.mock.calls[1][1] as TestRequest
  act(() =>
    client.setQueryData(['model-tests', 'history'], {
      ...history(),
      enabled: false,
    })
  )
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Retry the same request' })
    ).toBeDisabled()
  )
  await user.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(post.mock.calls[2][0]).toBe(
    `/api/user/model-tests/runs/${request.request_id}/cancel`
  )
  expect(
    post.mock.calls.filter(([url]) => String(url).endsWith('/runs'))
  ).toHaveLength(1)
})

test('a fresh failed history request never enables cached paid controls on a new mount', async () => {
  const get = vi.spyOn(api, 'get').mockRejectedValue(new Error('offline'))
  const post = vi.spyOn(api, 'post')
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  client.setQueryData(['model-tests', 'history'], history([run()]))
  client.setQueryData(['status'], { model_test_enabled: true })
  render(
    <QueryClientProvider client={client}>
      <IntelligenceDashboard />
    </QueryClientProvider>
  )
  expect(
    await screen.findByText(en.translation['fingerprint.availabilityUnknown'])
  ).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Self-test' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('heading', { name: 'My latest 60 tests' })
  ).not.toBeInTheDocument()
  expect(post).not.toHaveBeenCalled()
  expect(
    get.mock.calls.some(([url]) => String(url).endsWith('/bootstrap'))
  ).toBe(false)
})

test('closing key selection while an estimate is pending prevents its late response opening a cost confirmation', async () => {
  mockReads()
  let completeEstimate: () => void = () => undefined
  const post = vi.spyOn(api, 'post').mockImplementation(
    (_url, body) =>
      new Promise((resolve) => {
        completeEstimate = () =>
          resolve({
            data: {
              success: true,
              data: {
                ...(body as TestSelection),
                estimated_quota: 100,
                quota_per_unit: 500000,
                estimate_is_cap: false,
                timeout_seconds: 90,
                benchmark_id: 'local-fingerprint-v1',
                confirmation_token: 'late-dismissed',
                expires_at: Math.floor(Date.now() / 1000) + 300,
              },
            },
          })
      })
  )
  const user = userEvent.setup()
  renderWithQuery(<IntelligenceDashboard />)
  await beginEstimate(user)
  await user.click(screen.getByRole('button', { name: 'Cancel' }))
  await act(async () => completeEstimate())
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Self-test' })).toBeEnabled()
  )
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  expect(post).toHaveBeenCalledTimes(1)
})

test('the display page shows group models without search controls or a personal history section', async () => {
  mockReads([
    run({ model: 'old-personal-model', group: 'Personal-only group' }),
  ])
  renderWithQuery(<IntelligenceDashboard />)
  expect(
    await screen.findByRole('heading', { name: 'test-model' })
  ).toBeVisible()
  expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
  expect(
    screen.queryByRole('heading', { name: 'My latest 60 tests' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('heading', { name: 'Personal-only group' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Details' })
  ).not.toBeInTheDocument()
})
