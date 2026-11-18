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
import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { IntegrationSetup } from '../integration-wizard'

vi.mock('@/features/pricing/hooks/use-pricing-data', () => ({
  usePricingData: () => ({
    models: [
      {
        id: 1,
        model_name: 'gpt-4.1',
        vendor_name: 'OpenAI',
        quota_type: 0,
        enable_groups: ['default', 'personal-free'],
        supported_endpoint_types: ['openai-response'],
      },
      {
        id: 2,
        model_name: 'claude-sonnet-4',
        vendor_name: 'Anthropic',
        quota_type: 0,
        enable_groups: ['default'],
        supported_endpoint_types: ['anthropic'],
      },
    ],
    usableGroup: {
      default: { desc: 'Standard route', ratio: 1 },
      'personal-free': { desc: 'Personal offer', ratio: 0 },
    },
    groupRatio: { default: 1, 'personal-free': 0 },
    endpointMap: {
      'openai-response': { path: '/v1/responses', method: 'POST' },
      anthropic: { path: '/v1/messages', method: 'POST' },
    },
    isLoading: false,
    error: null,
  }),
}))
vi.mock('@/features/dashboard/hooks/use-status-data', () => ({
  useApiInfo: () => ({ items: [] }),
}))
vi.mock('../../help/lib/docs-utils', () => ({
  getDocsOrigin: () => 'https://gateway.example',
}))

async function renderWizard() {
  const rootRoute = createRootRoute({
    component: () => (
      <IntegrationSetup initialModel='gpt-4.1' initialGroup='personal-free' />
    ),
  })
  const router = createRouter({
    routeTree: rootRoute,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await act(async () => {
    render(<RouterProvider router={router} />)
    await router.load()
  })
}

describe('integration wizard flow', () => {
  it('preserves model and free group context through configuration and verification', async () => {
    const user = userEvent.setup()
    await renderWizard()
    expect(screen.getByRole('combobox', { name: 'Calling group' })).toHaveValue(
      'personal-free'
    )
    expect(screen.getByText('0×')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Next step' }))
    await user.click(screen.getByRole('combobox', { name: 'Client' }))
    await user.click(screen.getByRole('option', { name: 'Codex CLI' }))
    expect(screen.getByText(/wire_api = "responses"/)).toBeVisible()
    expect(screen.getByText(/env_key = "NEW_API_KEY"/)).toBeVisible()
    expect(
      screen.getByRole('button', { name: 'Download configuration' })
    ).toBeEnabled()
    await user.click(screen.getByRole('button', { name: 'Next step' }))
    expect(
      screen.getByRole('button', { name: 'Open usage logs' })
    ).toHaveAttribute('href', '/usage-logs')
    expect(
      screen.getByRole('button', { name: 'API Reference' })
    ).toHaveAttribute('href', '/help/api?endpoint=responses')
    expect(
      screen.getByText(/https:\/\/gateway.example\/v1\/responses/)
    ).toBeVisible()
    expect(screen.getByText('personal-free')).toBeVisible()
  })

  it('reconciles the group and model after switching providers', async () => {
    const user = userEvent.setup()
    await renderWizard()
    await user.click(screen.getByRole('combobox', { name: 'Model provider' }))
    await user.click(screen.getByRole('option', { name: 'Anthropic' }))
    expect(screen.getByRole('combobox', { name: 'Calling group' })).toHaveValue(
      'default'
    )
    expect(screen.getByRole('combobox', { name: 'Model' })).toHaveValue(
      'claude-sonnet-4'
    )
    await user.click(screen.getByRole('button', { name: 'Next step' }))
    await user.click(screen.getByRole('combobox', { name: 'Client' }))
    expect(
      screen.queryByRole('option', { name: 'Codex CLI' })
    ).not.toBeInTheDocument()
    expect(screen.getByRole('option', { name: 'Claude Code' })).toBeVisible()
  })
})
