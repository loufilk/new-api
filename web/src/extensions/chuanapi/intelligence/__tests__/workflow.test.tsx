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
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import { api } from '@/lib/api'

import { IntelligenceDashboard } from '../index'
import { ModelTestCard } from '../model-card'
import type { TestHistory, TestRun, TestSelection, TestRequest } from '../types'

function history(items: TestRun[] = []): TestHistory {
  return {
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
  return render(
    <QueryClientProvider client={client}>{content}</QueryClientProvider>
  )
}
function mockReads(items: TestRun[] = []) {
  return vi.spyOn(api, 'get').mockImplementation(async (url) => {
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
    return { data: { success: true, data: history(items) } }
  })
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
    expect(screen.getByText('No site results published')).toBeVisible()
    expect(post).not.toHaveBeenCalled()
    await user.click(
      screen.getByRole('button', { name: 'Review cost and start' })
    )
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
      max_output_tokens: 2048,
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
      await user.click(
        await screen.findByRole('button', { name: 'Review cost and start' })
      )
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
        expect(
          screen.getByRole('button', { name: 'Review cost and start' })
        ).toBeDisabled()
      } else {
        await waitFor(() =>
          expect(
            screen.getByRole('button', { name: 'Review cost and start' })
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
      await screen.findByRole('button', { name: 'Review cost and start' })
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
    await user.click(
      await screen.findByRole('button', { name: 'Review cost and start' })
    )
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
    await user.click(
      await screen.findByRole('button', { name: 'Review cost and start' })
    )
    await user.click(
      await screen.findByRole('button', { name: 'Accept charges and run once' })
    )
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Review cost and start' })
      ).toBeEnabled()
    )
    expect(
      screen.queryByRole('button', { name: 'Retry the same request' })
    ).not.toBeInTheDocument()
    expect(screen.getByText('Failed')).toBeVisible()
  })
  test('counts only completed renderable results and leaves empty timeline slots unrecorded', async () => {
    mockReads()
    const onSelect = vi.fn()
    renderWithQuery(
      <ModelTestCard
        model='test-model'
        group='default'
        runs={[
          run(),
          run({ id: 'failed', status: 'failed' }),
          run({ id: 'intercepted', status: 'intercepted' }),
          run({ id: 'invalid', assessment: 'invalid' }),
        ]}
        disabled={false}
        estimating={false}
        onEstimate={() => undefined}
        onSelect={onSelect}
      />
    )
    expect(
      screen.getByText('Structurally valid: 1 of 4 recent runs')
    ).toBeVisible()
    const timeline = screen.getByRole('list', { name: '60-slot test timeline' })
    expect(
      within(timeline).getAllByRole('listitem', { hidden: true })
    ).toHaveLength(60)
    expect(within(timeline).getAllByRole('button')).toHaveLength(4)
    await userEvent.click(
      screen.getByRole('button', { name: 'View latest valid result' })
    )
    expect(onSelect).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'test-run' })
    )
  })
  test('reports request failures without treating errors as valid model results', async () => {
    vi.spyOn(api, 'get').mockRejectedValue(new Error('Offline'))
    renderWithQuery(<IntelligenceDashboard />)
    expect(await screen.findByRole('button', { name: 'Retry' })).toBeVisible()
    expect(
      screen.queryByRole('button', { name: 'Review cost and start' })
    ).not.toBeInTheDocument()
  })
})
