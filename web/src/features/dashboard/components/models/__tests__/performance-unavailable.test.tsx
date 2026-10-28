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
import { QueryClientProvider, type QueryClient } from '@tanstack/react-query'
import {
  act,
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError, type AxiosAdapter } from 'axios'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { PerformanceOverview } from '@/features/dashboard/components/models/performance-overview'
import { PerformanceHealthPanel } from '@/features/dashboard/components/overview/performance-health-panel'
import { getPerfMetricsSummary } from '@/features/performance-metrics/api'
import { api } from '@/lib/api'
import { createAppQueryClient } from '@/lib/query-client'

const originalAdapter = api.defaults.adapter
const healthyResponse = {
  success: true,
  data: {
    summary: { success_rate: 99.01, avg_latency_ms: 1009, avg_tps: 5 },
    models: [
      {
        model_name: 'test-model',
        success_rate: 100,
        avg_latency_ms: 1000,
        avg_tps: 5,
      },
    ],
  },
}

let client: QueryClient
const redirect = vi.fn()

beforeEach(() => {
  redirect.mockClear()
  client = createAppQueryClient(redirect)
})

afterEach(async () => {
  cleanup()
  await client.cancelQueries()
  client.clear()
  api.defaults.adapter = originalAdapter
})

function setResponse(payload: unknown): void {
  api.defaults.adapter = async (config) => ({
    data: payload,
    status: 200,
    statusText: 'OK',
    headers: {},
    config,
  })
}

describe.each([
  ['Overview', PerformanceHealthPanel],
  ['Models', PerformanceOverview],
] as const)('%s optional performance panel', (_name, Component) => {
  function renderPanel(): void {
    render(
      <QueryClientProvider client={client}>
        <a href='/keys'>API keys</a>
        <Component />
      </QueryClientProvider>
    )
  }

  it('keeps the server-weighted summary when the response is valid', async () => {
    setResponse(healthyResponse)
    renderPanel()
    expect(await screen.findByText('99.01%')).toBeVisible()
    expect(screen.getByText('1.01s')).toBeVisible()
    expect(screen.getByText('test-model')).toBeVisible()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(redirect).not.toHaveBeenCalled()
  })

  it.each([
    ['null envelope', null],
    ['HTML proxy response', '<html>private proxy detail</html>'],
    ['null data', { success: true, data: null }],
    ['object models', { success: true, data: { models: {} } }],
    ['null model item', { success: true, data: { models: [null] } }],
    [
      'object model name',
      {
        success: true,
        data: {
          models: [
            {
              ...healthyResponse.data.models[0],
              model_name: { private: true },
            },
          ],
        },
      },
    ],
    [
      'string success rate',
      {
        success: true,
        data: {
          models: [{ ...healthyResponse.data.models[0], success_rate: '100' }],
        },
      },
    ],
    [
      'invalid summary',
      {
        success: true,
        data: {
          models: healthyResponse.data.models,
          summary: { success_rate: 'private detail' },
        },
      },
    ],
  ])(
    'shows an unavailable state, not zeros or an empty success, for %s',
    async (_case, payload) => {
      setResponse(payload)
      renderPanel()
      const failure = await screen.findByRole('alert', {
        name: 'Performance health',
      })
      expect(within(failure).getByText('Unavailable')).toBeVisible()
      expect(
        within(failure).getByRole('button', { name: 'Retry' })
      ).toBeEnabled()
      expect(
        screen.queryByText('No performance data available')
      ).not.toBeInTheDocument()
      expect(screen.queryByText('0.00%')).not.toBeInTheDocument()
      expect(screen.queryByText(/private/)).not.toBeInTheDocument()
      expect(screen.getByRole('link', { name: 'API keys' })).toBeVisible()
      expect(redirect).not.toHaveBeenCalled()
    }
  )

  it('validates a fresh malformed cache entry before rendering', async () => {
    client.setQueryData(['perf-metrics-summary', 24], {
      success: true,
      data: null,
    })
    renderPanel()
    expect(
      await screen.findByRole('alert', { name: 'Performance health' })
    ).toBeVisible()
    expect(redirect).not.toHaveBeenCalled()
  })

  it('keeps an HTTP 500 local to the optional panel without exposing the error payload', async () => {
    api.defaults.adapter = async (config) => {
      throw new AxiosError('HTTP 500', 'ERR_BAD_RESPONSE', config, undefined, {
        data: { message: 'private response body' },
        status: 500,
        statusText: 'Internal Server Error',
        headers: {},
        config,
      })
    }
    renderPanel()
    const failure = await screen.findByRole('alert', {
      name: 'Performance health',
    })
    expect(failure).toHaveAttribute('data-http-status', '500')
    expect(screen.queryByText(/private response/)).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'API keys' })).toBeVisible()
    expect(redirect).not.toHaveBeenCalled()
  })

  it('disables repeated retry while pending and recovers through the real query', async () => {
    setResponse({ success: false, message: 'private diagnostic' })
    renderPanel()
    const retry = await screen.findByRole('button', { name: 'Retry' })
    const user = userEvent.setup()
    let finishRequest: (() => void) | undefined
    const adapter = vi.fn<AxiosAdapter>(
      (config) =>
        new Promise((resolve) => {
          finishRequest = () =>
            resolve({
              data: healthyResponse,
              status: 200,
              statusText: 'OK',
              headers: {},
              config,
            })
        })
    )
    api.defaults.adapter = adapter
    try {
      retry.focus()
      await user.keyboard('{Enter}')
      await waitFor(() => expect(retry).toBeDisabled())
      await user.click(retry)
      expect(adapter).toHaveBeenCalledTimes(1)
      await act(async () => finishRequest?.())
      expect(await screen.findByText('99.01%')).toBeVisible()
      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
      expect(redirect).not.toHaveBeenCalled()
    } finally {
      // A failed assertion must not leave HTTP de-duplication pending for the
      // next independent case.
      finishRequest?.()
    }
  })

  it('does not present cached healthy values as current after a failed refetch', async () => {
    client.setQueryData(['perf-metrics-summary', 24], healthyResponse)
    setResponse({ success: false, message: 'temporarily unavailable' })
    renderPanel()
    expect(screen.getByText('99.01%')).toBeVisible()
    await act(async () => {
      await client.refetchQueries({
        queryKey: ['perf-metrics-summary', 24],
        exact: true,
      })
    })
    expect(
      await screen.findByRole('alert', { name: 'Performance health' })
    ).toBeVisible()
    expect(screen.queryByText('99.01%')).not.toBeInTheDocument()
  })
})

it('keeps the raw performance API contract for non-dashboard consumers', async () => {
  const payload = {
    success: true,
    data: { models: null },
    extension: 'preserved',
  }
  setResponse(payload)
  await expect(getPerfMetricsSummary()).resolves.toEqual(payload)
})
