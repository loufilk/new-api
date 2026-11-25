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
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import type { KeyGroupHealth } from '../../hooks/use-key-group-health'
import { KeyGroupCards } from '../key-group-cards'
import { KeyGroupHealthPreview } from '../key-group-health-preview'

const i18n = createInstance()
await i18n.init({
  lng: 'en',
  resources: { en: { translation: {} } },
  initAsync: false,
})

const emptyMeasurements = {
  request_count: 0,
  success_count: 0,
  success_rate: null,
  ttft_sample_count: 0,
  average_ttft_ms: null,
}

function snapshot(
  group: string,
  overrides: Partial<KeyGroupHealth> = {}
): KeyGroupHealth {
  const start = 1_800_000
  const data = {
    group,
    available: true,
    metrics_available: true,
    window_start: start,
    window_end: start + 23 * 3600 + 60,
    observed_at: start + 23 * 3600 + 60,
    latest_bucket_start: null,
    bucket_seconds: 3600,
    flush_interval_seconds: 120,
    ...emptyMeasurements,
    buckets: Array.from({ length: 24 }, (_, i) => ({
      bucket_start: start + i * 3600,
      ...emptyMeasurements,
    })),
    ...overrides,
  } satisfies KeyGroupHealth
  return data
}

function measuredSnapshot(group: string): KeyGroupHealth {
  const s = snapshot(group, {
    request_count: 11,
    success_count: 10,
    success_rate: 90.91,
    ttft_sample_count: 5,
    average_ttft_ms: 360,
  })
  s.buckets[0] = {
    ...s.buckets[0],
    request_count: 9,
    success_count: 9,
    success_rate: 100,
    ttft_sample_count: 4,
    average_ttft_ms: 200,
  }
  s.buckets[1] = {
    ...s.buckets[1],
    request_count: 1,
    success_rate: 0,
  }
  s.buckets[2] = {
    ...s.buckets[2],
    request_count: 1,
    success_count: 1,
    success_rate: 100,
    ttft_sample_count: 1,
    average_ttft_ms: 1000,
  }
  s.latest_bucket_start = s.buckets[2].bucket_start
  return s
}

function apiResponse(data: KeyGroupHealth) {
  return { data: { success: true, data } }
}

function signIn(id: number, sid: string) {
  useAuthStore.getState().auth.setBundle({
    user: { id, username: 'fixture', role: 1 },
    access_token: 'synthetic-test-token',
    token_type: 'Bearer',
    access_expires_at: 1000,
    session: {
      sid,
      current: true,
      login_method: 'fixture',
      ip: '',
      user_agent: '',
      created_at: 1,
      last_active_at: 1,
      expires_at: 1000,
    },
  })
}

let client: QueryClient
beforeEach(() => {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  signIn(2, 'synthetic-owner')
})

afterEach(() => {
  cleanup()
  client.clear()
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
})

function content(group: string) {
  return (
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={client}>
        <KeyGroupHealthPreview groupId={group} />
      </QueryClientProvider>
    </I18nextProvider>
  )
}

it('requests only the explicitly opened group; closing aborts the read', async () => {
  let aborted: AbortSignal | undefined
  const get = vi.spyOn(api, 'get').mockImplementation(async (_, config) => {
    aborted = config?.signal as AbortSignal
    return new Promise(() => {})
  })

  const matchMedia = window.matchMedia
  vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
    ...matchMedia(query),
    matches: query === '(max-width: 1023px)',
  }))
  const choose = vi.fn()
  const user = userEvent.setup()
  render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={client}>
        <KeyGroupCards
          groups={[
            {
              id: 'group + exact',
              label: 'Group A',
              description: 'First group',
              platform: 'openai',
              sortOrder: 0,
              ratio: 1,
            },
            {
              id: 'other',
              label: 'Group B',
              description: 'Second group',
              platform: 'other',
              sortOrder: 0,
              ratio: 2,
            },
          ]}
          value=''
          onChange={choose}
          previewDetails={KeyGroupHealthPreview}
        />
      </QueryClientProvider>
    </I18nextProvider>
  )
  expect(get).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Group A' }))
  expect(get).toHaveBeenCalledExactlyOnceWith('/api/token/group-health', {
    params: { group: 'group + exact' },
    signal: expect.any(AbortSignal),
    authSessionEpoch: expect.any(Number),
  })
  expect(choose).not.toHaveBeenCalled()
  expect(
    screen.getByRole('region', { name: '24-hour group health' })
  ).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Back to group list' }))
  expect(aborted?.aborted).toBe(true)
  expect(
    screen.queryByRole('region', { name: '24-hour group health' })
  ).not.toBeInTheDocument()
})

it('displays measured aggregate success and TTFT, not an average of the hourly percentages or means', async () => {
  vi.spyOn(api, 'get').mockResolvedValue(apiResponse(measuredSnapshot('g')))
  render(content('g'))

  expect(await screen.findByText('90.91%')).toBeVisible()
  expect(screen.getByText(/360\s*ms/)).toBeVisible()
  expect(screen.getByText('Requests: 11')).toBeVisible()
  expect(screen.getByText('Samples: 5')).toBeVisible()
  const chart = screen.getByRole('list', {
    name: 'Performance metrics for the last 24 hours',
  })
  const buckets = within(chart).getAllByRole('listitem')
  expect(buckets).toHaveLength(24)
  expect(buckets[1]).toHaveTextContent('Success rate: 0%')
  expect(buckets[1]).toHaveTextContent('Average TTFT: No data')
  expect(buckets[3]).toHaveTextContent('Success rate: No data')
  expect(screen.getByText(/Last checked:/)).toBeVisible()
  expect(screen.getByText(/Latest sampled hour:/)).toBeVisible()
})

it.each([
  [true, 'No requests were observed in this window.'],
  [false, 'Group health metrics are unavailable.'],
])(
  'separates empty telemetry from unavailable metrics (metrics available %s)',
  async (metrics_available, message) => {
    vi.spyOn(api, 'get').mockResolvedValue(
      apiResponse(snapshot('g', { metrics_available }))
    )
    render(content('g'))
    expect(await screen.findByText(message)).toBeVisible()
    expect(screen.getByText('Enabled routes')).toBeVisible()
    expect(screen.getByText('Yes')).toBeVisible()
    expect(screen.queryByText('0%')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('list', {
        name: 'Performance metrics for the last 24 hours',
      })
    ).not.toBeInTheDocument()
  }
)

it('discards a response for a different group and retries without rendering it', async () => {
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValueOnce(apiResponse(measuredSnapshot('wrong-group')))
    .mockResolvedValueOnce(apiResponse(snapshot('g')))
  render(content('g'))

  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Could not load group health.'
  )
  expect(screen.queryByText('90.91%')).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(
    await screen.findByText('No requests were observed in this window.')
  ).toBeVisible()
  expect(get).toHaveBeenCalledTimes(2)
})

it('switching group or session aborts the old read and never displays its metrics', async () => {
  const replies: Array<(value: ReturnType<typeof apiResponse>) => void> = []
  const signals: Array<AbortSignal | undefined> = []
  const get = vi.spyOn(api, 'get').mockImplementation(async (_, config) => {
    signals.push(config?.signal as AbortSignal)
    return new Promise((resolve) => replies.push(resolve))
  })
  const view = render(content('first'))
  await waitFor(() => expect(get).toHaveBeenCalledTimes(1))
  view.rerender(content('second'))
  await waitFor(() => expect(get).toHaveBeenCalledTimes(2))
  expect(signals[0]?.aborted).toBe(true)
  act(() => signIn(2, 'synthetic-next-session'))
  await waitFor(() => expect(get).toHaveBeenCalledTimes(3))
  expect(signals[1]?.aborted).toBe(true)
  await act(async () => {
    replies[0](apiResponse(measuredSnapshot('first')))
    replies[1](apiResponse(measuredSnapshot('second')))
    replies[2](apiResponse(snapshot('second')))
  })
  expect(
    await screen.findByText('No requests were observed in this window.')
  ).toBeVisible()
  expect(screen.queryByText('90.91%')).not.toBeInTheDocument()
})

it('does not retain cached measurements when a different owner has no access', async () => {
  vi.spyOn(api, 'get')
    .mockResolvedValueOnce(apiResponse(measuredSnapshot('g')))
    .mockResolvedValueOnce({
      data: { success: false, message: 'Access denied' },
    })
  render(content('g'))
  expect(await screen.findByText('90.91%')).toBeVisible()

  act(() => signIn(9, 'synthetic-owner'))
  expect(screen.queryByText('90.91%')).not.toBeInTheDocument()
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Could not load group health.'
  )
  expect(screen.queryByText('Access denied')).not.toBeInTheDocument()
})

it('does not query a virtual Auto group or a signed-out account', () => {
  const get = vi.spyOn(api, 'get')
  const view = render(content('auto'))
  expect(screen.queryByRole('region')).not.toBeInTheDocument()
  act(() => useAuthStore.getState().auth.reset())
  view.rerender(content('private-group'))
  expect(screen.queryByRole('region')).not.toBeInTheDocument()
  expect(get).not.toHaveBeenCalled()
})
