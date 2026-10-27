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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { ImageGenerationPage } from '../components/ImageGenerationPage'

let client: QueryClient
const name = 'grok-imagine-image-2.0'
const key = {
  id: 7,
  name: 'Current key',
  status: 1,
  group: 'default',
  model_limits_enabled: false,
  model_limits: '',
}
const hint =
  'No image models match this API key. Check the model image-generation endpoint configuration, the key group, and its model restrictions, then refresh.'

beforeEach(() => {
  localStorage.clear()
  client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  })
})
afterEach(() => {
  vi.restoreAllMocks()
  client.clear()
  localStorage.clear()
})

function renderPage(
  endpoints: string[],
  tokenModels = [name],
  selectedKey = key
) {
  client.setQueryData(['image-generation-bootstrap'], {
    keys: [selectedKey],
    pricingModels: [{ model_name: name, supported_endpoint_types: endpoints }],
  })
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === `/api/token/${selectedKey.id}/models`) {
      return { data: { success: true, data: tokenModels } }
    }
    throw new Error(`Unexpected request: ${url}`)
  })
  return render(
    <QueryClientProvider client={client}>
      <ImageGenerationPage />
    </QueryClientProvider>
  )
}

test.each([
  {
    label: 'missing image endpoint',
    endpoints: ['openai'],
    tokenModels: [name],
    selectedKey: key,
  },
  {
    label: 'group excludes the model',
    endpoints: ['image-generation'],
    tokenModels: [],
    selectedKey: key,
  },
  {
    label: 'token restricts the model',
    endpoints: ['image-generation'],
    tokenModels: [],
    selectedKey: {
      ...key,
      model_limits_enabled: true,
      model_limits: 'dall-e-3',
    },
  },
])(
  '$label shows configuration checks without enabling generation',
  async ({ endpoints, tokenModels, selectedKey }) => {
    renderPage(endpoints, tokenModels, selectedKey)
    expect(await screen.findByText(hint)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Generate' })).toBeDisabled()
  }
)

test('refresh reloads pricing, token limits and owned token models after configuration is repaired', async () => {
  renderPage(['openai'], [], {
    ...key,
    model_limits_enabled: true,
    model_limits: 'dall-e-3',
  })
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/pricing') {
      return {
        data: {
          success: true,
          data: [
            {
              model_name: name,
              supported_endpoint_types: ['image-generation'],
            },
          ],
        },
      }
    }
    if (url === '/api/token/?p=1&size=100') {
      return { data: { success: true, data: { items: [key], total: 1 } } }
    }
    if (url === '/api/token/7/models') {
      return { data: { success: true, data: [name] } }
    }
    throw new Error(`Unexpected request: ${url}`)
  })
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: 'Refresh' }))
  await waitFor(() =>
    expect(screen.getByLabelText('Model')).toHaveTextContent(name)
  )
  expect(screen.queryByText(hint)).not.toBeInTheDocument()
  await user.type(screen.getByLabelText('Prompt'), 'A lake')
  expect(screen.getByRole('button', { name: 'Generate' })).toBeEnabled()
})

test('the Console model enables reference upload, aspect ratio and quality selection', async () => {
  const consoleName = 'grok-imagine-image-2.0-console'
  renderPage(['image-generation'], [consoleName])
  client.setQueryData(['image-generation-bootstrap'], {
    keys: [key],
    pricingModels: [
      {
        model_name: consoleName,
        supported_endpoint_types: ['image-generation'],
      },
    ],
  })
  await waitFor(() =>
    expect(screen.getByLabelText('Model')).toHaveTextContent(consoleName)
  )
  expect(
    screen.getByRole('button', { name: 'Add reference images' })
  ).toBeEnabled()
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Size' }))
  await user.click(screen.getByRole('button', { name: '16:9' }))
  await user.keyboard('{Escape}')
  expect(screen.getByRole('button', { name: 'Size' })).toHaveTextContent('16:9')
  await user.click(screen.getByRole('button', { name: 'Quality' }))
  await user.click(screen.getByRole('button', { name: 'Medium' }))
  await user.keyboard('{Escape}')
  expect(screen.getByRole('button', { name: 'Quality' })).toHaveTextContent(
    'Medium'
  )
  expect(
    screen.getByText(
      'Console supports up to 3 reference images and low/medium quality. Size sets the aspect ratio; resolution is 1K.'
    )
  ).toBeInTheDocument()
})
