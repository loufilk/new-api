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
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import type { ReactNode } from 'react'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import type { ApiKey } from '../../types'
import { ApiKeysMutateDrawer } from '../api-keys-mutate-drawer'

const mocks = vi.hoisted(() => ({
  getUserGroups: vi.fn(),
  getTokenAutoGroups: vi.fn(),
  getApiKeyScopeModels: vi.fn(),
  createApiKey: vi.fn(),
  updateApiKey: vi.fn(),
  getApiKey: vi.fn(),
  triggerRefresh: vi.fn(),
}))

vi.mock('@/hooks/use-status', () => ({
  useStatus: () => ({
    status: { default_use_auto_group: false },
    loading: false,
  }),
}))

vi.mock('@/lib/api', () => ({
  getUserGroups: mocks.getUserGroups,
}))

vi.mock('../../api', () => ({
  createApiKey: mocks.createApiKey,
  updateApiKey: mocks.updateApiKey,
  getApiKey: mocks.getApiKey,
  getTokenAutoGroups: mocks.getTokenAutoGroups,
  getApiKeyScopeModels: mocks.getApiKeyScopeModels,
}))

vi.mock('../api-keys-provider', () => ({
  useApiKeys: () => ({ triggerRefresh: mocks.triggerRefresh }),
}))

vi.mock('../api-key-group-combobox', () => ({
  ApiKeyGroupCombobox: ({
    options,
    value,
    onValueChange,
  }: {
    options: Array<{ value: string }>
    value?: string
    onValueChange: (value: string) => void
  }) => (
    <div data-testid='group-picker'>
      {options.map((option) => (
        <button
          key={option.value}
          type='button'
          data-selected={value === option.value}
          onClick={() => onValueChange(option.value)}
        >
          {`group:${option.value}`}
        </button>
      ))}
    </div>
  ),
}))

vi.mock('@/components/multi-select', () => ({
  MultiSelect: ({
    options,
    selected,
    onChange,
  }: {
    options: Array<{ value: string; hint?: string }>
    selected: string[]
    onChange: (values: string[]) => void
  }) => (
    <div>
      <div data-testid='model-options'>
        {options.map((option) => (
          <div key={option.value}>
            <button
              type='button'
              onClick={() =>
                onChange(
                  selected.includes(option.value)
                    ? selected.filter((value) => value !== option.value)
                    : [...selected, option.value]
                )
              }
            >
              {`model:${option.value}`}
            </button>
            {option.hint && (
              <span>{`hint:${option.value}:${option.hint}`}</span>
            )}
          </div>
        ))}
      </div>
      <div data-testid='selected-models'>{selected.join(',')}</div>
    </div>
  ),
}))

vi.mock('@/components/ui/collapsible', () => ({
  Collapsible: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  CollapsibleTrigger: ({ children }: { children: ReactNode }) => (
    <div>{children}</div>
  ),
  CollapsibleContent: ({ children }: { children: ReactNode }) => (
    <div>{children}</div>
  ),
}))

vi.mock('@/components/ui/sheet', () => ({
  Sheet: ({ open, children }: { open: boolean; children: ReactNode }) =>
    open ? <div>{children}</div> : null,
  SheetContent: ({ children }: { children: ReactNode }) => (
    <div>{children}</div>
  ),
  SheetDescription: ({ children }: { children: ReactNode }) => (
    <div>{children}</div>
  ),
  SheetFooter: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  SheetHeader: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  SheetTitle: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  SheetClose: ({ children }: { children: ReactNode }) => <div>{children}</div>,
}))

vi.mock('@/components/datetime-picker', () => ({
  DateTimePicker: () => <div data-testid='date-time-picker' />,
}))

vi.mock(
  '@/features/system-settings/request-policies/related-policy-link',
  () => ({
    RelatedPolicyLink: () => null,
  })
)

const i18n = createInstance()
await i18n.init({
  lng: 'en',
  resources: { en: { translation: {} } },
  initAsync: false,
})

const restrictedKey: ApiKey = {
  id: 7,
  name: 'restricted',
  key: 'demo********1234',
  status: 1,
  remain_quota: 0,
  used_quota: 0,
  unlimited_quota: true,
  expired_time: -1,
  created_time: 0,
  accessed_time: 0,
  group: 'a',
  auto_groups: [],
  cross_group_retry: false,
  model_limits_enabled: true,
  model_limits: 'a-only',
  allow_ips: '',
  allow_user_quota_query: true,
}

let client: QueryClient

function renderDrawer(currentRow?: ApiKey) {
  return render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={client}>
        <ApiKeysMutateDrawer
          open
          onOpenChange={vi.fn()}
          currentRow={currentRow}
        />
      </QueryClientProvider>
    </I18nextProvider>
  )
}

beforeEach(() => {
  client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  mocks.getUserGroups.mockResolvedValue({
    success: true,
    data: {
      a: { desc: 'A', ratio: 1 },
      b: { desc: 'B', ratio: 1 },
    },
  })
  mocks.getTokenAutoGroups.mockResolvedValue({
    success: true,
    data: { groups: ['a', 'b'], max_count: 5 },
  })
  mocks.getApiKey.mockResolvedValue({
    success: true,
    data: restrictedKey,
  })
  mocks.createApiKey.mockResolvedValue({
    success: true,
    data: restrictedKey,
  })
  mocks.updateApiKey.mockResolvedValue({
    success: true,
    data: restrictedKey,
  })
  mocks.getApiKeyScopeModels.mockImplementation(
    async ({ group }: { group: string }) => {
      let data = ['a-only', 'shared']
      if (group === 'b') {
        data = ['b-only', 'shared']
      }
      return { success: true, data }
    }
  )
})

afterEach(() => {
  cleanup()
  client.clear()
  vi.clearAllMocks()
})

it('refreshes model candidates when the group changes', async () => {
  const user = userEvent.setup()
  renderDrawer()

  await waitFor(() =>
    expect(mocks.getApiKeyScopeModels).toHaveBeenCalledWith({
      group: '',
      auto_groups_mode: 'inherit',
      auto_groups: [],
    })
  )

  expect(
    await screen.findByRole('button', { name: 'model:a-only' })
  ).toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'model:b-only' })
  ).not.toBeInTheDocument()

  await user.click(screen.getByRole('button', { name: 'group:b' }))

  await waitFor(() =>
    expect(mocks.getApiKeyScopeModels).toHaveBeenLastCalledWith({
      group: 'b',
      auto_groups_mode: 'inherit',
      auto_groups: [],
    })
  )

  await waitFor(() =>
    expect(
      screen.queryByRole('button', { name: 'model:a-only' })
    ).not.toBeInTheDocument()
  )
  expect(
    screen.getByRole('button', { name: 'model:b-only' })
  ).toBeInTheDocument()
})

it('preserves a restricted whitelist when candidates disappear', async () => {
  const user = userEvent.setup()
  mocks.getApiKeyScopeModels.mockResolvedValue({
    success: true,
    data: [],
  })

  renderDrawer(restrictedKey)

  await waitFor(() =>
    expect(mocks.getApiKeyScopeModels).toHaveBeenCalledWith({
      group: 'a',
      auto_groups_mode: 'inherit',
      auto_groups: [],
    })
  )

  expect(screen.getByTestId('selected-models')).toHaveTextContent('a-only')
  expect(
    screen.getByText('hint:a-only:Unavailable for current routing scope')
  ).toBeInTheDocument()

  const nameInput = screen.getByPlaceholderText('Enter a name')
  await user.clear(nameInput)
  await user.type(nameInput, 'renamed')
  await user.click(screen.getByRole('button', { name: 'Save changes' }))

  await waitFor(() => expect(mocks.updateApiKey).toHaveBeenCalledTimes(1))
  expect(mocks.updateApiKey).toHaveBeenCalledWith(
    expect.objectContaining({
      id: restrictedKey.id,
      name: 'renamed',
      group: 'a',
      model_limits_enabled: true,
      model_limits: 'a-only',
    })
  )
})

it('keeps a restricted payload after switching groups', async () => {
  const user = userEvent.setup()
  renderDrawer(restrictedKey)

  await waitFor(() =>
    expect(mocks.getApiKeyScopeModels).toHaveBeenCalledWith({
      group: 'a',
      auto_groups_mode: 'inherit',
      auto_groups: [],
    })
  )

  await user.click(screen.getByRole('button', { name: 'group:b' }))

  await waitFor(() =>
    expect(mocks.getApiKeyScopeModels).toHaveBeenLastCalledWith({
      group: 'b',
      auto_groups_mode: 'inherit',
      auto_groups: [],
    })
  )

  expect(screen.getByTestId('selected-models')).toHaveTextContent('a-only')
  expect(
    screen.getByText('hint:a-only:Unavailable for current routing scope')
  ).toBeInTheDocument()
  expect(
    screen.getByRole('button', { name: 'model:b-only' })
  ).toBeInTheDocument()

  await user.click(screen.getByRole('button', { name: 'Save changes' }))

  await waitFor(() => expect(mocks.updateApiKey).toHaveBeenCalledTimes(1))
  expect(mocks.updateApiKey).toHaveBeenCalledWith(
    expect.objectContaining({
      id: restrictedKey.id,
      group: 'b',
      model_limits_enabled: true,
      model_limits: 'a-only',
    })
  )
})
