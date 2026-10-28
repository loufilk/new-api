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
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { ModelApiActions } from '../components/model-api-actions'
import type { ModelEndpointMap } from '../lib/model-endpoints'
import type { PricingModel } from '../types'

const base: PricingModel = {
  id: 1,
  model_name: 'MODEL_ID',
  quota_type: 0,
  model_ratio: 1,
  completion_ratio: 1,
  enable_groups: [],
}
async function renderActions(
  values: Partial<PricingModel>,
  endpointMap: ModelEndpointMap
) {
  const root = createRootRoute({
    component: () => (
      <ModelApiActions
        model={{ ...base, ...values }}
        endpointMap={endpointMap}
      />
    ),
  })
  const router = createRouter({
    routeTree: root,
    history: createMemoryHistory(),
  })
  render(<RouterProvider router={router} />)
  await screen.findByRole('link', { name: 'API Reference' })
}

describe('model API actions', () => {
  it('copies the actual video endpoint and links video docs without offering a nonexistent video playground', async () => {
    const user = userEvent.setup()
    const writeText = vi.spyOn(navigator.clipboard, 'writeText')
    await renderActions(
      {
        output_modalities: ['video'],
        supported_endpoint_types: ['openai', 'openai-video'],
      },
      {
        openai: { method: 'POST', path: '/v1/chat/completions' },
        'openai-video': { method: 'POST', path: '/v1/videos' },
      }
    )
    expect(screen.getByText('POST /v1/videos')).toBeVisible()
    expect(screen.getByRole('link', { name: 'API Reference' })).toHaveAttribute(
      'href',
      '/help/api?endpoint=videos-create'
    )
    expect(screen.queryByRole('link', { name: 'Playground' })).toBeNull()
    await user.click(screen.getByRole('button', { name: 'Copy URL' }))
    await waitFor(() =>
      expect(writeText).toHaveBeenCalledWith(
        `${window.location.origin}/v1/videos`
      )
    )
  })
  it.each<{
    label: string
    model: Partial<PricingModel>
    endpoint: string
    path: string
    documentation: string
  }>([
    {
      label: 'speech synthesis',
      model: { output_modalities: ['audio'] },
      endpoint: 'audio-speech',
      path: '/v1/audio/speech',
      documentation: 'audio-speech',
    },
    {
      label: 'speech recognition with text output',
      model: { input_modalities: ['audio'], output_modalities: ['text'] },
      endpoint: 'audio-transcription',
      path: '/v1/audio/transcriptions',
      documentation: 'audio-transcriptions',
    },
    {
      label: 'embeddings',
      model: { capabilities: ['embeddings'] },
      endpoint: 'embeddings',
      path: '/v1/embeddings',
      documentation: 'embeddings',
    },
    {
      label: 'reranking',
      model: { capabilities: ['rerank'] },
      endpoint: 'jina-rerank',
      path: '/v1/rerank',
      documentation: 'rerank',
    },
  ])(
    'prefers the declared $label endpoint over a default OpenAI protocol for URL and docs',
    async (fixture) => {
      const user = userEvent.setup()
      const writeText = vi.spyOn(navigator.clipboard, 'writeText')
      await renderActions(
        {
          ...fixture.model,
          supported_endpoint_types: ['openai', fixture.endpoint],
        },
        {
          openai: { path: '/v1/chat/completions' },
          [fixture.endpoint]: { path: fixture.path },
        }
      )
      expect(screen.getByText(`POST ${fixture.path}`)).toBeVisible()
      expect(
        screen.getByRole('link', { name: 'API Reference' })
      ).toHaveAttribute('href', `/help/api?endpoint=${fixture.documentation}`)
      expect(screen.queryByRole('link', { name: 'Playground' })).toBeNull()
      await user.click(screen.getByRole('button', { name: 'Copy URL' }))
      await waitFor(() =>
        expect(writeText).toHaveBeenCalledWith(
          `${window.location.origin}${fixture.path}`
        )
      )
    }
  )
  it('retains a declared chat playground but prioritizes the media endpoint for a multi-capability model', async () => {
    await renderActions(
      {
        output_modalities: ['text', 'video'],
        supported_endpoint_types: ['openai', 'openai-video'],
      },
      {
        openai: { path: '/v1/chat/completions' },
        'openai-video': { path: '/v1/videos' },
      }
    )
    expect(screen.getByText('POST /v1/videos')).toBeVisible()
    expect(screen.getByRole('link', { name: 'Playground' })).toHaveAttribute(
      'href',
      '/playground'
    )
  })
  it('does not invent a copyable URL when the declared endpoint is missing from the map', async () => {
    await renderActions({ supported_endpoint_types: ['openai-video'] }, {})
    expect(screen.queryByRole('button', { name: 'Copy URL' })).toBeNull()
    expect(screen.getByRole('link', { name: 'API Reference' })).toHaveAttribute(
      'href',
      '/help'
    )
  })
  it('opens the existing image workspace only for a declared image generation endpoint', async () => {
    await renderActions(
      { supported_endpoint_types: ['image-generation'] },
      { 'image-generation': { path: '/v1/images/generations' } }
    )
    expect(screen.getByRole('link', { name: 'Playground' })).toHaveAttribute(
      'href',
      '/image'
    )
  })
  it('opens the existing chat workspace for text output and a declared chat endpoint', async () => {
    await renderActions(
      { output_modalities: ['text'], supported_endpoint_types: ['openai'] },
      { openai: { path: '/v1/chat/completions' } }
    )
    expect(screen.getByRole('link', { name: 'Playground' })).toHaveAttribute(
      'href',
      '/playground'
    )
  })
  it('does not infer a chat URL or documentation from a default OpenAI protocol without text metadata', async () => {
    await renderActions(
      { supported_endpoint_types: ['openai'] },
      { openai: { path: '/v1/chat/completions' } }
    )
    expect(screen.queryByRole('link', { name: 'Playground' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Copy URL' })).toBeNull()
    expect(screen.getByRole('link', { name: 'API Reference' })).toHaveAttribute(
      'href',
      '/help'
    )
  })
})
