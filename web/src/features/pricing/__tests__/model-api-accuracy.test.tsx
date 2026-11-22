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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { STATUS_QUERY_KEY } from '@/lib/status-query'

import { ModelDetailsApi } from '../components/model-details-api'
import type { ModelEndpointMap } from '../lib/model-endpoints'
import type { PricingModel } from '../types'

const model: PricingModel = {
  id: 1,
  model_name: 'unknown',
  quota_type: 0,
  model_ratio: 1,
  completion_ratio: 1,
  enable_groups: ['default'],
}
async function renderApi(
  values: Partial<PricingModel> = {},
  endpointMap: ModelEndpointMap = {}
) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: Infinity, gcTime: 0 },
    },
  })
  queryClient.setQueryData(STATUS_QUERY_KEY, {})
  const root = createRootRoute({
    component: () => (
      <QueryClientProvider client={queryClient}>
        <ModelDetailsApi
          model={{ ...model, ...values }}
          endpointMap={endpointMap}
        />
      </QueryClientProvider>
    ),
  })
  const router = createRouter({
    routeTree: root,
    history: createMemoryHistory(),
  })
  render(<RouterProvider router={router} />)
  await screen.findByRole('heading', { name: 'Authentication' })
}
describe('model API contract accuracy', () => {
  it('does not show invented per-group rate limits or model-name-guessed parameter support', async () => {
    await renderApi()
    expect(screen.queryByText('RPM')).toBeNull()
    expect(screen.queryByText('temperature')).toBeNull()
  })
  it('shows the correct Gemini and Anthropic headers without forcing every protocol to use Bearer', async () => {
    await renderApi()
    expect(screen.getByText('x-goog-api-key: <TOKEN>')).toBeVisible()
    expect(screen.getByText('anthropic-version: 2023-06-01')).toBeVisible()
  })
  it.each<{
    endpoint: string
    path: string
    documentation: string
    model: Partial<PricingModel>
    payloadField: string
  }>([
    {
      endpoint: 'openai-video',
      path: '/v1/videos',
      documentation: 'videos-create',
      model: { output_modalities: ['video'] },
      payloadField: 'prompt',
    },
    {
      endpoint: 'embeddings',
      path: '/v1/embeddings',
      documentation: 'embeddings',
      model: { capabilities: ['embeddings'] },
      payloadField: 'input',
    },
    {
      endpoint: 'jina-rerank',
      path: '/v1/rerank',
      documentation: 'rerank',
      model: { capabilities: ['rerank'] },
      payloadField: 'documents',
    },
  ])(
    'defaults to the declared $endpoint sample and docs instead of the generic OpenAI protocol',
    async (fixture) => {
      const user = userEvent.setup()
      const writeText = vi.spyOn(navigator.clipboard, 'writeText')
      await renderApi(
        {
          ...fixture.model,
          supported_endpoint_types: ['openai', fixture.endpoint],
        },
        {
          openai: { path: '/v1/chat/completions' },
          [fixture.endpoint]: { path: fixture.path },
        }
      )
      expect(
        screen.getByRole('link', { name: 'API Reference' })
      ).toHaveAttribute('href', `/help/api?endpoint=${fixture.documentation}`)
      expect(screen.queryByRole('tab', { name: 'openai' })).toBeNull()
      await user.click(screen.getByRole('button', { name: 'Copy code' }))
      await waitFor(() =>
        expect(writeText).toHaveBeenCalledWith(
          expect.stringContaining(fixture.path)
        )
      )
      const copied = writeText.mock.calls[0][0]
      expect(copied).toContain(`"${fixture.payloadField}"`)
      expect(copied).not.toContain('/v1/chat/completions')
      expect(copied).not.toContain('"messages"')
      if (fixture.endpoint === 'jina-rerank') {
        expect(copied).toContain('"query"')
        expect(copied).not.toContain('"input"')
      }
    }
  )
  it.each<{
    endpoint: string
    path: string
    documentation: string
    model: Partial<PricingModel>
  }>([
    {
      endpoint: 'audio-speech',
      path: '/v1/audio/speech',
      documentation: 'audio-speech',
      model: { output_modalities: ['audio'] },
    },
    {
      endpoint: 'audio-transcription',
      path: '/v1/audio/transcriptions',
      documentation: 'audio-transcriptions',
      model: { input_modalities: ['audio'], output_modalities: ['text'] },
    },
  ])(
    'keeps $endpoint documentation without inventing a chat sample when no verified audio sample exists',
    async (fixture) => {
      await renderApi(
        {
          ...fixture.model,
          supported_endpoint_types: ['openai', fixture.endpoint],
        },
        {
          openai: { path: '/v1/chat/completions' },
          [fixture.endpoint]: { path: fixture.path },
        }
      )
      expect(
        screen.getByRole('link', { name: 'API Reference' })
      ).toHaveAttribute('href', `/help/api?endpoint=${fixture.documentation}`)
      expect(
        screen.getByText('No verified code sample for this endpoint.')
      ).toBeVisible()
      expect(screen.queryByRole('button', { name: 'Copy code' })).toBeNull()
    }
  )
  it('does not offer a chat sample or chat documentation for an unknown default OpenAI model', async () => {
    await renderApi(
      { supported_endpoint_types: ['openai'] },
      { openai: { path: '/v1/chat/completions' } }
    )
    expect(screen.queryByRole('heading', { name: 'Code samples' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Copy code' })).toBeNull()
    expect(screen.queryByRole('link', { name: 'API Reference' })).toBeNull()
  })
  it('does not use chat or embedding samples for an unknown explicit endpoint', async () => {
    await renderApi(
      { supported_endpoint_types: ['custom-protocol'] },
      { 'custom-protocol': { path: '/custom' } }
    )
    expect(
      screen.getByText('No verified code sample for this endpoint.')
    ).toBeVisible()
    expect(screen.getByRole('link', { name: 'API Reference' })).toHaveAttribute(
      'href',
      '/help'
    )
    expect(screen.queryByRole('button', { name: 'Copy code' })).toBeNull()
  })
  it.each(['first', 'last'] as const)(
    'prioritizes media and retains verified chat when generic OpenAI is %s on a multi-capability model',
    async (position) => {
      const user = userEvent.setup()
      await renderApi(
        {
          output_modalities: ['video'],
          capabilities: ['text_generation'],
          supported_endpoint_types:
            position === 'first'
              ? ['openai', 'openai-video']
              : ['openai-video', 'openai'],
        },
        {
          openai: { path: '/v1/chat/completions' },
          'openai-video': { path: '/v1/videos' },
        }
      )
      expect(screen.getByRole('tab', { name: 'openai-video' })).toHaveAttribute(
        'aria-selected',
        'true'
      )
      expect(
        screen.getByRole('link', { name: 'API Reference' })
      ).toHaveAttribute('href', '/help/api?endpoint=videos-create')
      await user.click(screen.getByRole('tab', { name: 'openai' }))
      expect(screen.getByRole('textbox', { name: 'bash' })).toHaveTextContent(
        '/v1/chat/completions'
      )
      expect(
        screen.getByRole('link', { name: 'API Reference' })
      ).toHaveAttribute('href', '/help/api?endpoint=chat-completions')
    }
  )
})
