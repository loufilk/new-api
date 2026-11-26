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
import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { normalizeWizardAddress } from '../../lib/integration-wizard'
import { IntegrationWizard } from '../integration-wizard-page'

const addressResponse = {
  success: true,
  data: {
    server_address: 'https://api.example.test',
    api_info: [
      { url: 'https://api.example.test/v1/', route: 'Global' },
      { url: 'https://asia.example.test/gateway/v1/', route: 'Asia' },
      { url: 'https://name:secret@invalid.test', route: 'Unusable' },
    ],
  },
}
vi.mock('@tanstack/react-router', async (actual) => ({
  ...(await actual<typeof import('@tanstack/react-router')>()),
  Link: ({
    to,
    search,
    children,
  }: {
    to: string
    search?: Record<string, string>
    children?: ReactNode
  }) => {
    const query = new URLSearchParams(search).toString()
    return <a href={query ? to + '?' + query : to}>{children}</a>
  },
}))

const fixture = {
  success: true,
  public_lines: [
    { id: 'line-opa-a', label: 'Claude east' },
    { id: 'line-opa-b', label: 'GPT east' },
    { id: 'line-opa-c', label: 'No supported client' },
  ],
  models: [
    {
      model_name: 'test-claude',
      public_line_ids: ['line-opa-a', 'line-opa-b'],
      supported_endpoint_types_by_public_line: {
        'line-opa-a': ['anthropic'],
        'line-opa-b': ['openai'],
      },
    },
    {
      model_name: 'test-gpt',
      public_line_ids: ['line-opa-b'],
      supported_endpoint_types_by_public_line: {
        'line-opa-b': ['openai-response'],
      },
    },
    {
      model_name: 'invalid-private',
      public_line_ids: [],
      supported_endpoint_types_by_public_line: {
        'line-opa-b': ['openai'],
      },
    },
    {
      model_name: 'embedding-only',
      public_line_ids: ['line-opa-c'],
      supported_endpoint_types_by_public_line: { 'line-opa-c': ['embedding'] },
    },
  ],
}

let queryClient: QueryClient
const request = vi.fn<typeof fetch>()

beforeEach(() => {
  request.mockReset()
  request.mockImplementation(async (url) =>
    Response.json(url === '/api/status' ? addressResponse : fixture)
  )
  vi.stubGlobal('fetch', request)
  queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
})

afterEach(() => {
  cleanup()
  queryClient.clear()
  vi.unstubAllGlobals()
})

function renderWizard(props = {}) {
  return render(
    <QueryClientProvider client={queryClient}>
      <IntegrationWizard {...props} />
    </QueryClientProvider>
  )
}

it('matches group-specific protocols, rejects secret or invalid URLs, and generates no paid requests', async () => {
  renderWizard({ publicLine: 'line-opa-b', previewModel: 'test-gpt' })
  const user = userEvent.setup()

  const group = await screen.findByRole('combobox', {
    name: 'Published model group',
  })
  expect(group).toHaveValue('line-opa-b')
  expect(
    within(group).queryByRole('option', { name: 'No supported client' })
  ).toBeNull()
  expect(screen.getByRole('combobox', { name: 'Model' })).toHaveValue(
    'test-gpt'
  )
  expect(screen.getByRole('combobox', { name: 'Client' })).toHaveValue('codex')
  expect(await screen.findByText(/wire_api = "responses"/)).toHaveTextContent(
    'https://api.example.test/v1'
  )
  expect(screen.getByRole('link', { name: 'API Keys' })).toHaveAttribute(
    'href',
    '/keys?create=true&publicLine=line-opa-b&model=test-gpt'
  )

  const address = screen.getByRole('combobox', { name: 'Connection address' })
  expect(within(address).queryByText('Unusable')).toBeNull()
  await user.selectOptions(
    address,
    within(address).getByRole('option', { name: 'Asia' })
  )
  expect(await screen.findByText(/wire_api = "responses"/)).toHaveTextContent(
    'https://asia.example.test/gateway/v1'
  )

  await user.selectOptions(group, 'line-opa-a')
  expect(screen.getByRole('combobox', { name: 'Model' })).toHaveValue(
    'test-claude'
  )
  expect(screen.getByRole('combobox', { name: 'Client' })).toHaveValue('claude')
  await user.selectOptions(
    screen.getByRole('combobox', { name: 'Operating system' }),
    'windows'
  )
  const setup = screen.getByText(/ANTHROPIC_BASE_URL/)
  expect(setup).toHaveTextContent(
    "$env:ANTHROPIC_BASE_URL = 'https://asia.example.test/gateway'"
  )
  expect(setup).not.toHaveTextContent('line-opa-a')
  expect(request).toHaveBeenCalledWith(
    '/public-api/model-lines',
    expect.objectContaining({
      credentials: 'omit',
      redirect: 'error',
      cache: 'no-store',
    })
  )
})

it('rejects stale or tampered deep links and never uses secret-like URL search values in snippets', async () => {
  renderWizard({
    publicLine: 'unpublished-private',
    previewModel: 'invalid-private',
  })
  expect(
    await screen.findByRole('combobox', { name: 'Published model group' })
  ).toHaveValue('line-opa-a')
  expect(screen.getByRole('combobox', { name: 'Model' })).toHaveValue(
    'test-claude'
  )
  expect(await screen.findByText(/ANTHROPIC_BASE_URL/)).not.toHaveTextContent(
    /unpublished-private|invalid-private/
  )
  expect(screen.getByRole('link', { name: 'API Keys' })).toHaveAttribute(
    'href',
    '/keys'
  )
  expect(request).toHaveBeenCalledTimes(2)
})

it('renders only placeholder examples if public data cannot be checked', async () => {
  request.mockImplementation(async (url) => {
    if (url === '/api/status') return Response.json(addressResponse)
    throw new Error('unavailable')
  })
  renderWizard({ publicLine: 'some-private-group', previewModel: 'test-gpt' })
  expect(
    await screen.findByText(/No published group is available/)
  ).toBeVisible()
  expect(
    screen.queryByRole('combobox', { name: 'Published model group' })
  ).toBeNull()
  expect(await screen.findByText(/ANTHROPIC_MODEL/)).toHaveTextContent(
    'MODEL_ID'
  )
  expect(screen.getByRole('link', { name: 'API Keys' })).toHaveAttribute(
    'href',
    '/keys'
  )
  expect(request).toHaveBeenCalledTimes(2)
})

it.each([
  ['https://api.example.test/base/v1/', 'https://api.example.test/base'],
  ['http://127.0.0.1:3000/v1beta', 'http://127.0.0.1:3000'],
  ['https://u:k@api.example.test', null],
  ['https://api.example.test?key=secret', null],
  ['https://api.example.test#private', null],
  ['http://api.example.test', null],
  ['javascript:alert(1)', null],
  ['//api.example.test', null],
])(
  'normalizes the configured endpoint %s without accepting secret input',
  (input, expected) => {
    expect(normalizeWizardAddress(input)).toBe(expected)
  }
)
