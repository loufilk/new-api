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
import { beforeEach, afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { ImageGenerationPage } from '../components/ImageGenerationPage'
import type { ImageHistoryEntry } from '../types'
import { HISTORY_OWNER_KEY, signInHistoryOwner } from './history-fixture'

const entry: ImageHistoryEntry = {
  id: 'old',
  prompt: 'Previous landscape',
  model: 'retired-model',
  settings: { size: '1536x1024', quality: 'high', style: '', count: 4 },
  createdAt: Date.now(),
  results: [
    {
      id: 'old-image',
      type: 'url',
      source: 'https://example.com/old.png',
      createdAt: Date.now(),
      keptLocally: false,
    },
  ],
}
let client: QueryClient
let tokenModels: string[]
beforeEach(() => {
  signInHistoryOwner()
  localStorage.clear()
  localStorage.setItem(HISTORY_OWNER_KEY, JSON.stringify([entry]))
  client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  })
  client.setQueryData(['image-generation-bootstrap'], {
    keys: [
      {
        id: 7,
        name: 'Current key',
        status: 1,
        group: 'test',
        model_limits_enabled: false,
        model_limits: '',
      },
    ],
    pricingModels: [
      {
        model_name: 'dall-e-3',
        supported_endpoint_types: ['image-generation'],
      },
    ],
  })
  tokenModels = ['dall-e-3']
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/token/7/models') {
      return { data: { success: true, data: tokenModels } }
    }
    throw new Error(`Unexpected request: ${url}`)
  })
})
afterEach(() => {
  vi.restoreAllMocks()
  client.clear()
  localStorage.clear()
  vi.unstubAllGlobals()
})
function renderPage() {
  return render(
    <QueryClientProvider client={client}>
      <ImageGenerationPage />
    </QueryClientProvider>
  )
}

test('reusing an unavailable historical model retains current key and clamps settings without generating', async () => {
  const post = vi.spyOn(api, 'post')
  renderPage()
  await waitFor(() =>
    expect(screen.getByLabelText('Model')).toHaveTextContent('dall-e-3')
  )
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: 'Reuse' }))
  expect(screen.getByLabelText('Prompt')).toHaveValue('Previous landscape')
  expect(screen.getByLabelText('Model')).toHaveTextContent('dall-e-3')
  expect(screen.getByLabelText('Count')).toHaveTextContent('1')
  expect(screen.getByLabelText('Token')).toHaveTextContent('Current key')
  expect(post).not.toHaveBeenCalled()
})

test('generation resets filters and inserts a single new result without losing old artwork', async () => {
  vi.spyOn(api, 'post').mockImplementation(async (url) => {
    if (url === '/api/token/7/key') {
      return { data: { success: true, data: { key: 'fixture' } } }
    }
    if (url === '/v1/images/generations') {
      return { data: { data: [{ url: 'https://example.com/new.png' }] } }
    }
    throw new Error('Unexpected request')
  })
  renderPage()
  const user = userEvent.setup()
  await user.type(screen.getByLabelText('Search history'), 'no-match')
  await user.type(screen.getByLabelText('Prompt'), 'New landscape')
  await user.click(screen.getByRole('button', { name: 'Generate' }))
  await screen.findByAltText('New landscape')
  expect(screen.getAllByAltText('New landscape')).toHaveLength(1)
  expect(screen.getByAltText('Previous landscape')).toBeInTheDocument()
  expect(screen.getByLabelText('Search history')).toHaveValue('')
})

test('reusing with reference images requires confirmation before clearing them', async () => {
  client.setQueryData(['image-generation-bootstrap'], {
    keys: [
      {
        id: 7,
        name: 'Current key',
        status: 1,
        group: 'test',
        model_limits_enabled: false,
        model_limits: '',
      },
    ],
    pricingModels: [
      {
        model_name: 'gpt-image-1',
        supported_endpoint_types: ['image-generation'],
      },
    ],
  })
  tokenModels = ['gpt-image-1']
  vi.stubGlobal(
    'URL',
    class extends URL {
      static createObjectURL() {
        return 'blob:reference'
      }
      static revokeObjectURL() {}
    }
  )
  renderPage()
  const user = userEvent.setup()
  await waitFor(() =>
    expect(screen.getByLabelText('Model')).toHaveTextContent('gpt-image-1')
  )
  await user.click(
    screen.getAllByRole('button', { name: 'Add reference images' })[0]
  )
  const input = document.querySelector<HTMLInputElement>('input[type=file]')
  if (!input) throw new Error('Missing reference file input')
  await user.upload(
    input,
    new File(['image'], 'reference.png', { type: 'image/png' })
  )
  expect(
    screen.getAllByRole('button', { name: 'Add reference images' })[0]
  ).toHaveTextContent('1')
  await user.keyboard('{Escape}')
  await user.click(screen.getByRole('button', { name: 'Reuse' }))
  await user.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Cancel',
    })
  )
  expect(
    screen.getAllByRole('button', { name: 'Add reference images' })[0]
  ).toHaveTextContent('1')
  expect(screen.getByLabelText('Prompt')).toHaveValue('')
  await user.keyboard('{Escape}')
  await user.click(screen.getByRole('button', { name: 'Reuse' }))
  await user.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Continue',
    })
  )
  expect(
    screen.queryByAltText('Reference image reference.png')
  ).not.toBeInTheDocument()
  expect(screen.getByLabelText('Prompt')).toHaveValue('Previous landscape')
  vi.unstubAllGlobals()
})

test('empty upstream response retains prompt and previous artwork with a retry hint', async () => {
  vi.spyOn(api, 'post').mockImplementation(async (url) => {
    if (url === '/api/token/7/key') {
      return { data: { success: true, data: { key: 'fixture' } } }
    }
    return { data: { data: [] } }
  })
  renderPage()
  const user = userEvent.setup()
  await user.type(screen.getByLabelText('Prompt'), 'New landscape')
  await user.click(screen.getByRole('button', { name: 'Generate' }))
  await screen.findByText('No images were returned. Try adjusting your prompt.')
  expect(screen.getByLabelText('Prompt')).toHaveValue('New landscape')
  expect(screen.getByAltText('Previous landscape')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Generate' })).toBeEnabled()
})

test('changing the authenticated account clears the editor and historical preview without adopting old history', async () => {
  renderPage()
  const user = userEvent.setup()
  await user.type(screen.getByLabelText('Prompt'), 'Private A draft')
  expect(screen.getByAltText('Previous landscape')).toBeInTheDocument()
  act(() => signInHistoryOwner(202))
  expect(screen.getByLabelText('Prompt')).toHaveValue('')
  expect(screen.queryByAltText('Previous landscape')).not.toBeInTheDocument()
  act(() => signInHistoryOwner(101))
  expect(await screen.findByAltText('Previous landscape')).toBeInTheDocument()
})
