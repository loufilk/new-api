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
import { useState } from 'react'
import { Toaster } from 'sonner'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { SettingsPageProvider } from '../../components/settings-page-context'
import { ModelTestSection } from '../model-test-section'
import type { ModelTestSettings } from '../model-test-settings'

function Settings() {
  const [actions, setActions] = useState<HTMLDivElement | null>(null)
  return (
    <SettingsPageProvider actionsContainer={actions}>
      <div ref={setActions} />
      <ModelTestSection />
      <Toaster />
    </SettingsPageProvider>
  )
}
function renderSettings(role = 10, overrides: Partial<ModelTestSettings> = {}) {
  useAuthStore.getState().auth.setUser({ id: 7, username: 'admin', role })
  const settings: ModelTestSettings = {
    engine_ready: true,
    enabled: false,
    version: 1,
    automatic_enabled: false,
    default_period_secs: 3600,
    daily_budget_quota: 0,
    targets: [],
    catalogue: {
      groups: ['default', 'premium'],
      models_by_group: { default: ['gpt-test'], premium: ['gpt-premium'] },
      routes: [
        { id: 1, name: 'Primary', group: 'default', model: 'gpt-test' },
        { id: 2, name: 'Premium', group: 'premium', model: 'gpt-premium' },
      ],
    },
    ...overrides,
  }
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValue({ data: { success: true, data: settings } })
  const put = vi.spyOn(api, 'put').mockImplementation(async (_, input) => {
    Object.assign(settings, input, { version: settings.version + 1 })
    return { data: { success: true, data: settings } }
  })
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: Infinity },
      mutations: { retry: false },
    },
  })
  client.setQueryData(['status'], {
    model_test_enabled: false,
    account_pool_enabled: true,
  })
  return {
    user: userEvent.setup(),
    client,
    get,
    put,
    settings,
    ...render(
      <QueryClientProvider client={client}>
        <Settings />
      </QueryClientProvider>
    ),
  }
}
afterEach(() => {
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
})

test.each([10, 100])(
  'role %s can save only dedicated fingerprint settings with expected version',
  async (role) => {
    const { user, put, get, client } = renderSettings(role)
    const toggle = await screen.findByRole('switch', {
      name: 'Enable model performance tests',
    })
    expect(toggle).not.toBeChecked()
    expect(screen.getByRole('button', { name: 'Save Changes' })).toBeDisabled()
    toggle.focus()
    await user.keyboard(' ')
    expect(put).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Save Changes' }))
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith(
        '/api/model-tests/settings',
        expect.objectContaining({
          enabled: true,
          expected_version: 1,
          automatic_enabled: false,
        })
      )
    )
    expect(
      get.mock.calls.every(([url]) => url === '/api/model-tests/settings')
    ).toBe(true)
    expect(client.getQueryData(['status'])).toMatchObject({
      model_test_enabled: true,
      account_pool_enabled: true,
    })
  }
)

test.each([1, 5])('role %s cannot view or request admin settings', (role) => {
  const { get, put } = renderSettings(role)
  expect(screen.queryByRole('switch')).not.toBeInTheDocument()
  expect(get).not.toHaveBeenCalled()
  expect(put).not.toHaveBeenCalled()
})

test('group and model selection enables automatic tests without token or budget controls', async () => {
  const { user, put } = renderSettings(10, {
    enabled: true,
    catalogue: {
      groups: ['default', 'premium'],
      models_by_group: { default: ['gpt-test'], premium: ['gpt-premium'] },
      routes: [],
    },
  })
  const automatic = await screen.findByRole('switch', {
    name: 'Enable automatic fingerprint tests',
  })
  expect(automatic).toHaveAttribute('aria-disabled', 'true')
  await user.click(screen.getByRole('button', { name: 'Add target' }))
  expect(screen.getByRole('button', { name: 'Save Changes' })).toBeDisabled()
  await user.selectOptions(screen.getByLabelText('Group'), 'default')
  await user.selectOptions(screen.getByLabelText('Model'), 'gpt-test')
  expect(
    screen.queryByRole('option', { name: 'gpt-premium' })
  ).not.toBeInTheDocument()
  expect(screen.queryByLabelText('Channel')).not.toBeInTheDocument()
  expect(
    screen.queryByLabelText('Maximum quota reserved per run')
  ).not.toBeInTheDocument()
  expect(
    screen.queryByLabelText('Maximum output tokens per call')
  ).not.toBeInTheDocument()
  expect(screen.queryByLabelText('Daily quota budget')).not.toBeInTheDocument()
  expect(automatic).not.toHaveAttribute('aria-disabled', 'true')
  await user.click(automatic)
  expect(put).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith(
      '/api/model-tests/settings',
      expect.objectContaining({
        automatic_enabled: true,
        daily_budget_quota: 0,
        targets: [
          expect.objectContaining({
            group: 'default',
            model: 'gpt-test',
            channel_id: 0,
            max_run_quota: 0,
            max_output_tokens: 0,
            period_secs: 0,
          }),
        ],
      })
    )
  )
})

test('changing group clears the model and prevents saving stale selections', async () => {
  const { user } = renderSettings(10, {
    targets: [
      {
        id: '9ab0a56f-f0bf-46de-afae-e4874c62ed03',
        group: 'default',
        model: 'gpt-test',
        channel_id: 1,
        enabled: true,
        period_secs: 0,
        max_output_tokens: 2048,
        max_run_quota: 100,
      },
    ],
  })
  await user.selectOptions(await screen.findByLabelText('Group'), 'premium')
  expect(screen.getByLabelText('Model')).toHaveValue('')
  expect(screen.queryByLabelText('Channel')).not.toBeInTheDocument()
  expect(
    screen.queryByRole('option', { name: 'gpt-test' })
  ).not.toBeInTheDocument()
  expect(
    screen.getByRole('option', { name: 'gpt-premium' })
  ).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Save Changes' })).toBeDisabled()
  await user.click(screen.getByRole('button', { name: 'Remove target' }))
  expect(screen.queryByLabelText('Group')).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Save Changes' })).toBeEnabled()
})

test('failed versioned save preserves the draft and reports the error', async () => {
  const { user, put } = renderSettings()
  put.mockResolvedValue({
    data: { success: false, message: 'Settings changed; reload and try again' },
  })
  await user.click(
    await screen.findByRole('switch', {
      name: 'Enable model performance tests',
    })
  )
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))
  expect(
    await screen.findByText('Settings changed; reload and try again')
  ).toBeVisible()
  expect(
    screen.getByRole('switch', { name: 'Enable model performance tests' })
  ).toBeChecked()
})

test('unavailable settings fail closed rather than presenting cached writable controls', async () => {
  const { client, get } = renderSettings()
  await screen.findByRole('switch', { name: 'Enable model performance tests' })
  get.mockRejectedValue(new Error('offline'))
  await client.invalidateQueries({ queryKey: ['model-test-settings'] })
  expect(await screen.findByText('Failed to load settings')).toBeVisible()
  expect(screen.queryByRole('switch')).not.toBeInTheDocument()
})

test('missing fingerprint reference data disables automatic enable even with valid budgets', async () => {
  renderSettings(10, {
    engine_ready: false,
    enabled: true,
    daily_budget_quota: 1000,
    targets: [
      {
        id: '9ab0a56f-f0bf-46de-afae-e4874c62ed03',
        group: 'default',
        model: 'gpt-test',
        channel_id: 1,
        enabled: true,
        period_secs: 0,
        max_output_tokens: 2048,
        max_run_quota: 100,
      },
    ],
  })
  expect(
    await screen.findByRole('switch', {
      name: 'Enable automatic fingerprint tests',
    })
  ).toHaveAttribute('aria-disabled', 'true')
  expect(
    screen.getByText(
      'Fingerprint reference data is not ready. Automatic tests cannot be enabled.'
    )
  ).toBeVisible()
})

test('admins see group routing guidance before saving or enabling tests', async () => {
  const { put } = renderSettings()
  expect(
    await screen.findByText(
      'Choose a group and a model within that group. Tests use normal group routing. The default interval is one hour. Each group and model can have only one target.'
    )
  ).toBeVisible()
  expect(put).not.toHaveBeenCalled()
})

test('saving legacy pinned targets uses normal group routing without selecting a channel', async () => {
  const { user, put } = renderSettings(10, {
    targets: [
      {
        id: '9ab0a56f-f0bf-46de-afae-e4874c62ed03',
        group: 'default',
        model: 'gpt-test',
        channel_id: 99,
        enabled: true,
        period_secs: 0,
        max_output_tokens: 2048,
        max_run_quota: 100,
      },
    ],
    catalogue: {
      groups: ['default'],
      models_by_group: { default: ['gpt-test'] },
      routes: [],
    },
  })
  await user.click(
    await screen.findByRole('switch', {
      name: 'Enable model performance tests',
    })
  )
  expect(screen.queryByLabelText('Channel')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith(
      '/api/model-tests/settings',
      expect.objectContaining({
        targets: [
          expect.objectContaining({
            group: 'default',
            model: 'gpt-test',
            channel_id: 0,
          }),
        ],
      })
    )
  )
})
