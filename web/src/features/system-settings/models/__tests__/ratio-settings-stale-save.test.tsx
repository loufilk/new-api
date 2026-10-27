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
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { AxiosError, type AxiosResponse } from 'axios'
import { toast } from 'sonner'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import { SettingsPageProvider } from '../../components/settings-page-context'
import { RatioSettingsCard } from '../ratio-settings-card'

vi.mock('sonner', () => ({
  toast: {
    error: vi.fn(),
    success: vi.fn(),
    info: vi.fn(),
    warning: vi.fn(),
    loading: vi.fn(),
    promise: vi.fn(),
    custom: vi.fn(),
    dismiss: vi.fn(),
  },
}))

const { renameSystemGroupsMock } = vi.hoisted(() => ({
  renameSystemGroupsMock: vi.fn(),
}))

vi.mock('../../api', () => ({
  renameSystemGroups: renameSystemGroupsMock,
}))

vi.mock('../../hooks/use-update-option', () => ({
  useUpdateOption: () => ({ mutateAsync: vi.fn(), isPending: false }),
}))

vi.mock('@/features/model-pricing/api', () => ({
  useModelPricing: () => ({
    data: undefined,
    isError: false,
    error: null,
    refetch: vi.fn(),
  }),
  useSaveModelPricing: () => ({
    mutateAsync: vi.fn(),
    isPending: false,
    isError: false,
    isSuccess: false,
    reset: vi.fn(),
  }),
  buildPricingChanges: () => [],
}))

const MODEL_DEFAULTS = {
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

const GROUP_DEFAULTS = {
  GroupRatio: '{\n  "vip": 1\n}',
  TopupGroupRatio: '{}',
  UserUsableGroups: '{"vip":"VIP"}',
  GroupGroupRatio: '{}',
  AutoGroups: '[]',
  MaxTokenAutoGroups: 5,
  DefaultUseAutoGroup: false,
  GroupSpecialUsableGroup: '{}',
}

const STALE_MESSAGE =
  'The group settings changed on the server. Reload the page and apply your edits again.'

let actionsContainer: HTMLDivElement | null = null

afterEach(() => {
  actionsContainer?.remove()
  actionsContainer = null
})

function renderCard() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
  actionsContainer = document.createElement('div')
  document.body.appendChild(actionsContainer)

  render(
    <QueryClientProvider client={queryClient}>
      <SettingsPageProvider actionsContainer={actionsContainer}>
        <RatioSettingsCard
          modelDefaults={MODEL_DEFAULTS}
          groupDefaults={GROUP_DEFAULTS}
          toolPricesDefault='{}'
          visibleTabs={['groups']}
        />
      </SettingsPageProvider>
    </QueryClientProvider>
  )
  return { queryClient, invalidateSpy }
}

function staleAxiosError() {
  const response = {
    data: {
      success: false,
      code: 'GROUP_RENAME_STALE',
      message: 'group settings changed since they were loaded: GroupRatio',
    },
    status: 409,
    statusText: 'Conflict',
    headers: {},
    config: {},
  } as AxiosResponse
  return new AxiosError(
    'Request failed with status code 409',
    'ERR_BAD_REQUEST',
    undefined,
    undefined,
    response
  )
}

async function renameVipAndSave() {
  fireEvent.change(await screen.findByDisplayValue('vip'), {
    target: { value: 'vip2' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Save group settings' }))
}

describe('group settings save conflict handling', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  test('an HTTP 409 stale conflict shows the specific message and preserves edits and mappings', async () => {
    renameSystemGroupsMock.mockRejectedValue(staleAxiosError())
    const { invalidateSpy } = renderCard()

    await renameVipAndSave()

    await waitFor(() => expect(renameSystemGroupsMock).toHaveBeenCalledTimes(1))
    await waitFor(() =>
      expect(vi.mocked(toast.error)).toHaveBeenCalledWith(STALE_MESSAGE)
    )
    // The stale conflict must not invalidate/refetch the options query: doing
    // so resets the form and silently discards the user's edits and mapping.
    expect(invalidateSpy).not.toHaveBeenCalledWith({
      queryKey: ['system-options'],
    })
    expect(screen.getByDisplayValue('vip2')).toBeInTheDocument()

    // Retrying keeps the explicit mapping instead of dropping it.
    fireEvent.click(screen.getByRole('button', { name: 'Save group settings' }))
    await waitFor(() => expect(renameSystemGroupsMock).toHaveBeenCalledTimes(2))
    const retryPayload = renameSystemGroupsMock.mock.calls[1]?.[0] as {
      renames: Array<{ from: string; to: string }>
    }
    expect(retryPayload.renames).toEqual([{ from: 'vip', to: 'vip2' }])
  })

  test('a successful save refreshes the baseline so the next save is a no-op', async () => {
    renameSystemGroupsMock.mockResolvedValue({
      success: true,
      message: '',
      data: {
        renames: [{ from: 'vip', to: 'vip2' }],
        options: 2,
        channels: 0,
        abilities: 0,
        users: 0,
        tokens: 0,
        subscription_plans: 0,
        user_subscriptions: 0,
        applied_options: {
          GroupRatio: '{"vip2":1}',
          UserUsableGroups: '{"vip2":"VIP"}',
        },
      },
    })
    const { invalidateSpy } = renderCard()

    await renameVipAndSave()

    await waitFor(() =>
      expect(vi.mocked(toast.success)).toHaveBeenCalledWith(
        'Setting updated successfully'
      )
    )
    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: ['system-options'],
    })

    fireEvent.click(screen.getByRole('button', { name: 'Save group settings' }))
    await waitFor(() =>
      expect(vi.mocked(toast.info)).toHaveBeenCalledWith('No changes to save')
    )
    expect(renameSystemGroupsMock).toHaveBeenCalledTimes(1)
  })
})
