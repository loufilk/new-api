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
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import en from '../../i18n/locales/en.json'
import zh from '../../i18n/locales/zh.json'
import { MonitorDashboard } from '../dashboard'
import type { MonitorResult, MonitorSnapshot } from '../types'

const result: MonitorResult = {
  id: 'a'.repeat(64),
  target_id: 'route-1',
  benchmark: 'pelican',
  status: 'ready',
  quality_status: 'passed',
  quality_reason: 'A clear composition',
  revision: 0,
  started_at: 1_800_000_000,
  completed_at: 1_800_000_030,
  duration_ms: 30_000,
}

function fixture(): MonitorSnapshot {
  return {
    enabled: true,
    running: false,
    stale: false,
    fetched_at: Math.floor(Date.now() / 1000),
    observed_at: Math.floor(Date.now() / 1000),
    targets: [{ id: 'route-1', number: 1, model: 'example-model' }],
    results: [result],
  }
}

function renderDashboard() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  return render(
    <QueryClientProvider client={client}>
      <MonitorDashboard />
    </QueryClientProvider>
  )
}

beforeEach(async () => {
  i18next.addResourceBundle('en', 'translation', en.translation, true, true)
  i18next.addResourceBundle('zhCN', 'translation', zh.translation, true, true)
  await i18next.changeLanguage('en')
})

describe('Model performance dashboard', () => {
  test('shows administrator page text and finds a group by its custom name', async () => {
    const data = fixture()
    data.title = 'Custom monitor'
    data.description = 'Our reference results'
    data.targets[0].name = 'Premium group'
    vi.spyOn(api, 'get').mockResolvedValue({ data: { success: true, data } })
    renderDashboard()
    expect(
      await screen.findByRole('heading', { name: 'Custom monitor' })
    ).toBeVisible()
    expect(screen.getByText('Our reference results')).toBeVisible()
    await userEvent.type(screen.getByRole('textbox'), 'Premium')
    expect(screen.getByText('Premium group')).toBeVisible()
    expect(screen.getByRole('heading', { name: 'example-model' })).toBeVisible()
  })
  test('shows loading and prevents duplicate manual refreshes until the response arrives', async () => {
    let resolve!: (value: {
      data: { success: boolean; data: MonitorSnapshot }
    }) => void
    vi.spyOn(api, 'get').mockImplementation(
      () =>
        new Promise((done) => {
          resolve = done
        })
    )
    renderDashboard()
    expect(screen.getByText('Loading benchmark results…')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Refresh data' })).toBeDisabled()
    resolve({ data: { success: true, data: fixture() } })
    expect(
      await screen.findByRole('heading', { name: 'example-model' })
    ).toBeVisible()
    expect(screen.getByRole('button', { name: 'Refresh data' })).toBeEnabled()
  })

  test('searches models, shows an empty result, and loads details using only local endpoints', async () => {
    const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
      if (String(url).includes('/results/')) {
        return {
          data: {
            success: true,
            data: {
              prompt: 'Create an illustration',
              answer: '<script>alert(1)</script>',
              reference_reason: 'Reference explanation',
            },
          },
        }
      }
      return { data: { success: true, data: fixture() } }
    })
    const user = userEvent.setup()
    renderDashboard()
    expect(
      await screen.findByRole('heading', { name: 'example-model' })
    ).toBeVisible()
    expect(screen.getByRole('note')).toHaveTextContent(
      'They do not measure every route on this site'
    )
    expect(screen.getByRole('img')).toHaveAttribute(
      'src',
      `/api/model-monitor/thumbnails/${result.id}?v=0`
    )
    await user.type(
      screen.getByRole('textbox', { name: 'Search models' }),
      'missing'
    )
    expect(screen.getByText('No matching monitored models')).toBeVisible()
    await user.clear(screen.getByRole('textbox', { name: 'Search models' }))
    await user.click(
      screen.getByRole('button', { name: 'View animated artwork' })
    )
    const dialog = await screen.findByRole('dialog', {
      name: 'Visual creation',
    })
    expect(
      await within(dialog).findByText('Create an illustration')
    ).toBeVisible()
    expect(within(dialog).queryByText('Model answer')).not.toBeInTheDocument()
    expect(dialog.querySelector('script')).toBeNull()
    expect(within(dialog).getByRole('img')).toHaveAttribute(
      'src',
      `/api/model-monitor/artworks/${result.id}?v=0`
    )
    for (const [url, options] of get.mock.calls) {
      expect(url).toMatch(/^\/api\/model-monitor/)
      expect(options).toMatchObject({
        baseURL: '',
        preserveAuthorization: true,
        skipAuthRefresh: true,
      })
    }
    await user.keyboard('{Escape}')
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
  })

  test('shows reasoning as text and reports a stale detail response', async () => {
    const snapshot = fixture()
    snapshot.results = [
      { ...result, benchmark: 'candy', status: 'passed', expected_answer: 29 },
    ]
    vi.spyOn(api, 'get').mockImplementation(async (url) => {
      if (String(url).includes('/results/')) {
        return {
          data: {
            success: true,
            stale: true,
            data: {
              prompt: 'Question',
              answer: '<script>alert(1)</script>',
              reference_reason: 'Reason',
            },
          },
        }
      }
      return { data: { success: true, data: snapshot } }
    })
    const user = userEvent.setup()
    renderDashboard()
    await user.click(
      await screen.findByRole('button', { name: /Logical reasoning/ })
    )
    const dialog = await screen.findByRole('dialog', {
      name: 'Logical reasoning',
    })
    expect(
      await within(dialog).findByText('<script>alert(1)</script>')
    ).toBeVisible()
    expect(dialog.querySelector('script')).toBeNull()
    expect(within(dialog).getByText('Expected answer: 29')).toBeVisible()
    expect(
      within(dialog).getByText(
        'These details are cached. The latest result has not synced.'
      )
    ).toBeVisible()
  })

  test('marks a failed refresh as cached and removes expired results', async () => {
    const snapshot = fixture()
    const get = vi
      .spyOn(api, 'get')
      .mockResolvedValue({ data: { success: true, data: snapshot } })
    const user = userEvent.setup()
    renderDashboard()
    await screen.findByRole('heading', { name: 'example-model' })
    get.mockRejectedValue(new Error('offline'))
    await user.click(screen.getByRole('button', { name: 'Refresh data' }))
    expect(await screen.findByText('Showing cached data')).toBeVisible()
    expect(screen.getByRole('heading', { name: 'example-model' })).toBeVisible()
    get.mockResolvedValue({
      data: {
        success: true,
        data: { ...snapshot, fetched_at: 1, stale: true },
      },
    })
    await user.click(screen.getByRole('button', { name: 'Refresh data' }))
    expect(
      await screen.findByText('Monitoring data is unavailable')
    ).toBeVisible()
    expect(
      screen.queryByRole('heading', { name: 'example-model' })
    ).not.toBeInTheDocument()
  })

  test('shows a disabled page without mistaking the empty timestamp for an outage', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: {
          ...fixture(),
          enabled: false,
          fetched_at: 0,
          results: [],
          targets: [],
        },
      },
    })
    renderDashboard()
    expect(
      await screen.findByText('Model monitoring is not currently available')
    ).toBeVisible()
    expect(
      screen.queryByText('Monitoring data is unavailable')
    ).not.toBeInTheDocument()
  })

  test('handles a business error, then lets the visitor retry', async () => {
    const get = vi
      .spyOn(api, 'get')
      .mockResolvedValue({ data: { success: false, message: 'Unavailable' } })
    const user = userEvent.setup()
    renderDashboard()
    expect(
      await screen.findByText('Monitoring data is unavailable')
    ).toBeVisible()
    get.mockResolvedValue({ data: { success: true, data: fixture() } })
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    expect(
      await screen.findByRole('heading', { name: 'example-model' })
    ).toBeVisible()
  })

  test('expands history, distinguishes unreviewed artwork, and handles image failures', async () => {
    const snapshot = fixture()
    snapshot.results = Array.from({ length: 5 }, (_, index) => ({
      ...result,
      id: String(index).repeat(64),
      quality_status: '',
      started_at: result.started_at - index,
    }))
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: snapshot },
    })
    const user = userEvent.setup()
    renderDashboard()
    await screen.findByRole('heading', { name: 'example-model' })
    expect(screen.getAllByText('Awaiting review')).toHaveLength(4)
    expect(screen.queryByText('Passed')).not.toBeInTheDocument()
    const expand = screen.getByRole('button', { name: 'Show all results' })
    expect(expand).toHaveAttribute('aria-expanded', 'false')
    await user.click(expand)
    expect(screen.getAllByText('Awaiting review')).toHaveLength(5)
    expect(
      screen.getByRole('button', { name: 'Show fewer results' })
    ).toHaveAttribute('aria-expanded', 'true')
    fireEvent.error(screen.getByRole('img'))
    expect(screen.getByText('Artwork is unavailable')).toBeVisible()
  })

  test('updates the interface when the visitor changes language', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: fixture() },
    })
    renderDashboard()
    await screen.findByRole('heading', { name: 'example-model' })
    await i18next.changeLanguage('zhCN')
    expect(
      await screen.findByRole('heading', { name: '模型能力监测' })
    ).toBeVisible()
    expect(screen.getByRole('textbox', { name: '搜索模型' })).toBeVisible()
  })
})
