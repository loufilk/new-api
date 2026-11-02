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
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { AxiosError, type AxiosResponse } from 'axios'
import i18next from 'i18next'
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

const { renameSystemGroupsMock, getSystemOptionsMock } = vi.hoisted(() => ({
  renameSystemGroupsMock: vi.fn(),
  getSystemOptionsMock: vi.fn(),
}))

vi.mock('../../api', () => ({
  renameSystemGroups: renameSystemGroupsMock,
  getSystemOptions: getSystemOptionsMock,
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
  fireEvent.click(
    await screen.findByRole('button', { name: 'Rename and update API keys' })
  )
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('group settings save conflict handling', () => {
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

    const firstRequestId = renameSystemGroupsMock.mock.calls[0][0].request_id
    expect(firstRequestId).toMatch(/^[A-Za-z0-9_-]{16,64}$/)

    // Retrying keeps the explicit mapping instead of dropping it.
    fireEvent.click(
      screen.getByRole('button', { name: 'Rename and update API keys' })
    )
    await waitFor(() => expect(renameSystemGroupsMock).toHaveBeenCalledTimes(2))
    const retryPayload = renameSystemGroupsMock.mock.calls[1]?.[0] as {
      renames: Array<{ from: string; to: string }>
    }
    expect(retryPayload.renames).toEqual([{ from: 'vip', to: 'vip2' }])
    expect(renameSystemGroupsMock.mock.calls[1][0].request_id).toBe(
      firstRequestId
    )
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
        'Groups renamed: 0 API keys, 0 users and 0 channels updated.'
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

test('network-failed renames reuse the request ID until the reviewed payload changes', async () => {
  renameSystemGroupsMock.mockRejectedValue(
    new AxiosError('Network Error', 'ERR_NETWORK')
  )
  renderCard()
  await renameVipAndSave()
  await waitFor(() => expect(renameSystemGroupsMock).toHaveBeenCalledTimes(1))
  const firstRequest = renameSystemGroupsMock.mock.calls[0][0]
  expect(firstRequest.request_id).toMatch(/^[A-Za-z0-9_-]{16,64}$/)
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Rename and update API keys' })
    ).toBeEnabled()
  )
  fireEvent.click(
    screen.getByRole('button', { name: 'Rename and update API keys' })
  )
  await waitFor(() => expect(renameSystemGroupsMock).toHaveBeenCalledTimes(2))
  expect(renameSystemGroupsMock.mock.calls[1][0]).toEqual(firstRequest)

  fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
  fireEvent.change(screen.getByDisplayValue('vip2'), {
    target: { value: 'gold' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Save group settings' }))
  fireEvent.click(
    await screen.findByRole('button', { name: 'Rename and update API keys' })
  )
  await waitFor(() => expect(renameSystemGroupsMock).toHaveBeenCalledTimes(3))
  expect(renameSystemGroupsMock.mock.calls[2][0].request_id).not.toBe(
    firstRequest.request_id
  )
  expect(renameSystemGroupsMock.mock.calls[2][0].renames).toEqual([
    { from: 'vip', to: 'gold' },
  ])
})

test('a request ID conflict asks the administrator to reload and preserves the draft', async () => {
  renameSystemGroupsMock.mockResolvedValue({
    success: false,
    code: 'GROUP_RENAME_REQUEST_CONFLICT',
    message: 'request conflict',
  })
  renderCard()
  await renameVipAndSave()

  await waitFor(() =>
    expect(toast.error).toHaveBeenCalledWith(
      'This rename request was already used for different changes. Reload the page before trying again.'
    )
  )
  expect(screen.getByDisplayValue('vip2')).toBeInTheDocument()
  expect(screen.getByRole('alertdialog')).toBeInTheDocument()
})

test('ordinary group setting edits save without a rename confirmation or request ID', async () => {
  renameSystemGroupsMock.mockResolvedValue({ success: true, message: '' })
  renderCard()
  fireEvent.click(screen.getByRole('button', { name: 'Switch to JSON' }))
  fireEvent.input(screen.getByLabelText('Group ratios'), {
    target: { value: '{"vip":2}' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Save group settings' }))

  await waitFor(() => expect(renameSystemGroupsMock).toHaveBeenCalledTimes(1))
  expect(renameSystemGroupsMock.mock.calls[0][0]).toEqual({
    renames: [],
    options: { GroupRatio: '{"vip":2}' },
    expected: { GroupRatio: GROUP_DEFAULTS.GroupRatio },
  })
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
})

test.each([
  ['en', '1,234'],
  ['zhCN', '1,234'],
  ['zhTW', '1,234'],
  ['ja', '1,234'],
  ['fr', '1\u202f234'],
  ['ru', '1\u00a0234'],
  ['vi', '1.234'],
])(
  'rename counts use the correct number format for interface locale %s',
  async (locale, formatted) => {
    const key =
      'Groups renamed: {{tokens}} API keys, {{users}} users and {{channels}} channels updated.'
    i18next.addResourceBundle(locale, 'translation', { [key]: key })
    await i18next.changeLanguage(locale)
    try {
      renameSystemGroupsMock.mockResolvedValue({
        success: true,
        message: '',
        data: { tokens: 1234, users: 1234, channels: 1234 },
      })
      renderCard()
      await renameVipAndSave()
      await waitFor(() =>
        expect(toast.success).toHaveBeenCalledWith(
          `Groups renamed: ${formatted} API keys, ${formatted} users and ${formatted} channels updated.`
        )
      )
    } finally {
      i18next.removeResourceBundle(locale, 'translation')
      await i18next.changeLanguage('en')
    }
  }
)

const replayReceipt = {
  success: true,
  message: '',
  data: {
    replayed: true,
    tokens: 3,
    users: 2,
    channels: 1,
    applied_options: {
      GroupRatio: '{"vip2":1}',
      UserUsableGroups: '{"vip2":"VIP"}',
    },
  },
}

const currentOptions = {
  success: true,
  message: '',
  data: [
    { key: 'GroupRatio', value: '{"enterprise":2}' },
    { key: 'UserUsableGroups', value: '{"enterprise":"Current group"}' },
    { key: 'TopupGroupRatio', value: '{}' },
    { key: 'GroupGroupRatio', value: '{}' },
    { key: 'AutoGroups', value: '[]' },
    { key: 'MaxTokenAutoGroups', value: '7' },
    { key: 'DefaultUseAutoGroup', value: 'false' },
    { key: 'group_ratio_setting.group_special_usable_group', value: '{}' },
  ],
}

test('replayed receipts load current settings instead of restoring an older operation snapshot', async () => {
  renameSystemGroupsMock.mockResolvedValue(replayReceipt)
  getSystemOptionsMock.mockResolvedValue(currentOptions)
  renderCard()
  await renameVipAndSave()

  await screen.findByDisplayValue('enterprise')
  expect(screen.queryByDisplayValue('vip2')).not.toBeInTheDocument()
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  expect(getSystemOptionsMock).toHaveBeenCalledWith({ fresh: true })
  fireEvent.click(screen.getByRole('button', { name: 'Save group settings' }))
  await waitFor(() =>
    expect(toast.info).toHaveBeenCalledWith('No changes to save')
  )
  expect(renameSystemGroupsMock).toHaveBeenCalledTimes(1)
})

test('failed refresh after a replay blocks stale edits and retries only the read, never the completed rename', async () => {
  renameSystemGroupsMock.mockResolvedValue(replayReceipt)
  getSystemOptionsMock
    .mockRejectedValueOnce(new AxiosError('Network Error', 'ERR_NETWORK'))
    .mockResolvedValueOnce(currentOptions)
  renderCard()
  await renameVipAndSave()

  await screen.findByText(
    'The rename completed, but current group settings could not be loaded. Reload before making more changes.'
  )
  expect(
    screen.queryByRole('button', { name: 'Save group settings' })
  ).not.toBeInTheDocument()
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
  await screen.findByDisplayValue('enterprise')
  expect(getSystemOptionsMock).toHaveBeenCalledTimes(2)
  expect(renameSystemGroupsMock).toHaveBeenCalledTimes(1)
})

test('a settings read started before receipt replay cannot overwrite the fresh replay baseline', async () => {
  renameSystemGroupsMock.mockResolvedValue(replayReceipt)
  getSystemOptionsMock.mockResolvedValue(currentOptions)
  const { queryClient } = renderCard()
  let finishOldRead: (value: typeof currentOptions) => void = () => {}
  const oldResult = new Promise<typeof currentOptions>((resolve) => {
    finishOldRead = resolve
  })
  const oldRead = queryClient
    .fetchQuery({ queryKey: ['system-options'], queryFn: () => oldResult })
    .catch(() => undefined)
  await renameVipAndSave()
  await screen.findByDisplayValue('enterprise')

  await act(async () => {
    finishOldRead({
      ...currentOptions,
      data: [{ key: 'GroupRatio', value: '{"vip2":1}' }],
    })
    await oldRead
  })

  expect(queryClient.getQueryData(['system-options'])).toEqual(currentOptions)
  expect(screen.getByDisplayValue('enterprise')).toBeInTheDocument()
  expect(screen.queryByDisplayValue('vip2')).not.toBeInTheDocument()
})

test('an incomplete current-options response cannot reopen a stale editor after replay', async () => {
  renameSystemGroupsMock.mockResolvedValue(replayReceipt)
  getSystemOptionsMock.mockResolvedValue({
    success: true,
    message: '',
    data: [{ key: 'GroupRatio', value: '{"enterprise":2}' }],
  })
  renderCard()
  await renameVipAndSave()

  await screen.findByText(
    'The rename completed, but current group settings could not be loaded. Reload before making more changes.'
  )
  expect(
    screen.queryByRole('button', { name: 'Save group settings' })
  ).not.toBeInTheDocument()
  expect(renameSystemGroupsMock).toHaveBeenCalledTimes(1)
})
