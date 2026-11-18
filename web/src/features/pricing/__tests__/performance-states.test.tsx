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
import { render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { ModelDetailsPerformance } from '../components/model-details-performance'

vi.mock('@visactor/react-vchart', () => ({ VChart: () => null }))
let client: QueryClient
beforeEach(() => {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
})
afterEach(() => client.clear())

const model = {
  id: 1,
  model_name: 'example-model',
  quota_type: 0,
  model_ratio: 1,
  completion_ratio: 3,
  enable_groups: ['default'],
}

describe('model performance observations', () => {
  it('offers retry on a failed request instead of reporting an empty sample', async () => {
    vi.spyOn(api, 'get').mockRejectedValue(new Error('upstream unavailable'))
    render(
      <QueryClientProvider client={client}>
        <ModelDetailsPerformance model={model} />
      </QueryClientProvider>
    )
    expect(await screen.findByRole('button', { name: 'Retry' })).toBeVisible()
    expect(
      screen.queryByText(
        'Performance data is not yet available for this model.'
      )
    ).not.toBeInTheDocument()
  })

  it('labels failed hourly samples as sampling windows instead of incidents', async () => {
    const sample = {
      ts: 1700000000,
      avg_ttft_ms: 40,
      avg_latency_ms: 100,
      success_rate: 98,
      avg_tps: 30,
    }
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: {
          model_name: model.model_name,
          summary: sample,
          series: [sample],
          groups: [{ group: 'default', ...sample, series: [sample] }],
        },
      },
    })
    render(
      <QueryClientProvider client={client}>
        <ModelDetailsPerformance model={model} />
      </QueryClientProvider>
    )
    expect(
      await screen.findByText('1 sampling windows with failures')
    ).toBeVisible()
    expect(screen.queryByText(/incidents/i)).not.toBeInTheDocument()
    expect(
      screen.getByRole('img', { name: 'Sampled success rate 98.00%' })
    ).toBeVisible()
  })
})
