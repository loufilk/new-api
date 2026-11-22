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
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'

import type { ModelPricingConfig } from '@/features/model-pricing/api'
import { pricingOptions } from '@/features/model-pricing/pricing'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { SettingsPageProvider } from '../../components/settings-page-context'
import { RatioSettingsCard } from '../ratio-settings-card'

const modelDefaults = {
  ModelPrice: '{}',
  ModelRatio: '{}',
  CacheRatio: '{}',
  CreateCacheRatio: '{}',
  CompletionRatio: '{}',
  ImageRatio: '{}',
  AudioRatio: '{}',
  AudioCompletionRatio: '{}',
  ExposeRatioEnabled: true,
  BillingMode: '{}',
  BillingExpr: '{}',
  PluginBillingExpr: '{}',
}
const groupDefaults = {
  GroupRatio: '{}',
  TopupGroupRatio: '{}',
  UserUsableGroups: '{}',
  GroupGroupRatio: '{}',
  AutoGroups: '[]',
  MaxTokenAutoGroups: 5,
  DefaultUseAutoGroup: false,
  GroupSpecialUsableGroup: '{}',
}

function snapshot(price: number, version: string): ModelPricingConfig {
  const values = { ModelPrice: price }
  return {
    entries: [
      { model_name: 'example', version, configured: values, effective: values },
    ],
    empty_version: 'empty',
    options: pricingOptions({ ModelPrice: JSON.stringify({ example: price }) }),
  }
}

async function confirmSave() {
  fireEvent.click(screen.getByRole('button', { name: /Save model prices/ }))
  const dialog = await screen.findByRole('alertdialog', {
    name: 'Confirm price changes',
  })
  fireEvent.click(
    within(dialog).getByRole('button', { name: 'Confirm and save' })
  )
}

afterEach(() => {
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
  localStorage.clear()
})

test('a background refetch does not rebase a draft and an explicit reload uses the new revision', async ({
  onTestFinished,
}) => {
  let current = snapshot(1, 'version-1')
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'admin', role: ROLE.SUPER_ADMIN })
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/option/model_pricing') {
      return { data: { success: true, data: current } }
    }
    return { data: { success: true, data: [], vendors: [] } }
  })
  const patch = vi
    .spyOn(api, 'patch')
    .mockResolvedValueOnce({
      data: { success: false, message: 'Pricing changed since loading' },
    })
    .mockImplementationOnce(async () => {
      current = snapshot(4, 'version-3')
      return { data: { success: true } }
    })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const actions = document.createElement('div')
  onTestFinished(() => {
    client.clear()
    actions.remove()
  })
  document.body.appendChild(actions)
  const content = () => (
    <QueryClientProvider client={client}>
      <SettingsPageProvider actionsContainer={actions}>
        <RatioSettingsCard
          modelDefaults={{ ...modelDefaults }}
          groupDefaults={{ ...groupDefaults }}
          toolPricesDefault='{}'
          visibleTabs={['models']}
        />
      </SettingsPageProvider>
    </QueryClientProvider>
  )
  const view = render(content())
  fireEvent.click(await screen.findByRole('button', { name: 'Switch to JSON' }))
  const fixed = () => screen.getByLabelText('Model fixed pricing')
  fireEvent.input(fixed(), { target: { value: '{"example":2}' } })

  current = snapshot(3, 'version-2')
  await act(async () => {
    await client.refetchQueries({ queryKey: ['model-pricing-config'] })
  })
  view.rerender(content())
  expect(fixed()).toHaveValue('{"example":2}')
  await confirmSave()
  await waitFor(() =>
    expect(patch).toHaveBeenNthCalledWith(1, '/api/option/model_pricing', {
      changes: [
        {
          model_name: 'example',
          expected_version: 'version-1',
          pricing: { ModelPrice: 2 },
        },
      ],
    })
  )
  expect(fixed()).toHaveValue('{"example":2}')
  fireEvent.click(await screen.findByRole('button', { name: 'Reload pricing' }))
  await waitFor(() =>
    expect(fixed()).toHaveValue(JSON.stringify({ example: 3 }, null, 2))
  )
  fireEvent.input(fixed(), { target: { value: '{"example":4}' } })
  await confirmSave()
  await waitFor(() =>
    expect(patch).toHaveBeenNthCalledWith(2, '/api/option/model_pricing', {
      changes: [
        {
          model_name: 'example',
          expected_version: 'version-2',
          pricing: { ModelPrice: 4 },
        },
      ],
    })
  )
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: /Save model prices/ })
    ).toBeDisabled()
  )
})
