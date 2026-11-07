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
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider, initReactI18next } from 'react-i18next'
import { Toaster, toast } from 'sonner'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import type { ApiKey } from '../../types'
import { ApiKeysMutateDrawer } from '../api-keys-mutate-drawer'
import { ApiKeysProvider } from '../api-keys-provider'

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

const availableGroups = {
  default: { desc: 'Standard access', ratio: 1 },
  vip: { desc: 'Priority access', ratio: 2 },
  auto: { desc: 'Automatic routing', ratio: 'auto' },
}
const legacyKey: ApiKey = {
  id: 7,
  name: 'inherited key',
  key: 'masked-fixture',
  status: 1,
  remain_quota: 0,
  used_quota: 0,
  unlimited_quota: true,
  expired_time: -1,
  created_time: 1,
  accessed_time: 0,
  group: '',
  auto_groups: [],
  cross_group_retry: false,
  model_limits_enabled: false,
  model_limits: '',
  allow_ips: '',
  allow_user_quota_query: true,
}

let client: QueryClient

function renderDrawer(
  options: {
    defaultAuto?: boolean
    noGroups?: boolean
    currentRow?: ApiKey
  } = {}
) {
  const server = {
    groups: options.noGroups
      ? {}
      : (availableGroups as Record<
          string,
          { desc: string; ratio: string | number }
        >),
    groupsFail: false,
    createToken: async (_data: unknown, _attempt: number) => ({
      data: { success: true, data: {} },
    }),
  }
  const created: Array<Record<string, unknown>> = []
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/status') {
      return {
        data: { data: { default_use_auto_group: !!options.defaultAuto } },
      }
    }
    if (url === '/api/user/self/groups') {
      if (server.groupsFail) throw new Error('Group lookup failed')
      return { data: { success: true, data: server.groups } }
    }
    if (url === '/api/token/auto-groups') {
      return {
        data: {
          success: true,
          data: { groups: ['vip', 'default'], max_count: 3 },
        },
      }
    }
    if (url === '/api/token/7') {
      return { data: { success: true, data: options.currentRow } }
    }
    throw new Error(`Unexpected GET ${url}`)
  })
  const post = vi.spyOn(api, 'post').mockImplementation(async (url, data) => {
    if (url === '/api/token/models') {
      return { data: { success: true, data: ['fixture-model'] } }
    }
    expect(url).toBe('/api/token/')
    created.push(data as Record<string, unknown>)
    return server.createToken(data, created.length)
  })
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  const close = vi.fn()
  const onCreated = vi.fn()
  client.setQueryData(['status'], {
    default_use_auto_group: !!options.defaultAuto,
  })
  render(
    <QueryClientProvider client={client}>
      <I18nextProvider i18n={i18n}>
        <ApiKeysProvider>
          <ApiKeysMutateDrawer
            open
            currentRow={options.currentRow}
            onOpenChange={close}
            onCreated={onCreated}
          />
        </ApiKeysProvider>
        <Toaster />
      </I18nextProvider>
    </QueryClientProvider>
  )
  return { server, get, post, put, created, close, onCreated }
}

async function selectGroup(
  user: ReturnType<typeof userEvent.setup>,
  group: string
) {
  await user.click(screen.getByRole('combobox', { name: 'Group' }))
  await user.click(
    screen.getByRole('option', { name: new RegExp(`^${group}`) })
  )
  await user.keyboard('{Escape}')
}

beforeEach(() => {
  client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  useAuthStore
    .getState()
    .auth.setUser({ id: 7, username: 'fixture', role: 1, group: 'default' })
})

afterEach(async () => {
  toast.dismiss()
  cleanup()
  await client.cancelQueries()
  client.clear()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
  localStorage.clear()
})

test.each([false, true])(
  'new keys remain unselected when the site Auto preference is %s',
  async (defaultAuto) => {
    const { post } = renderDrawer({ defaultAuto })
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Add' })).toBeEnabled()
    )
    expect(screen.getByRole('combobox', { name: 'Group' })).toHaveTextContent(
      'Select groups in routing order'
    )
    expect(screen.getByRole('combobox', { name: 'Group' })).toHaveAttribute(
      'aria-required',
      'true'
    )
    expect(
      screen.getByRole('combobox', { name: /Select models/ })
    ).toBeDisabled()
    expect(post).not.toHaveBeenCalled()
  }
)

test('submitting a new key without selecting a group shows the field error and sends no request', async () => {
  const { post } = renderDrawer()
  const user = userEvent.setup()
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Add' })).toBeEnabled()
  )
  await user.type(
    screen.getByRole('textbox', { name: 'Token Name' }),
    'needs a group'
  )
  await user.click(screen.getByRole('button', { name: 'Add' }))
  expect(await screen.findByText('Please select a group')).toBeVisible()
  expect(screen.getByRole('combobox', { name: 'Group' })).toHaveAttribute(
    'aria-invalid',
    'true'
  )
  expect(post).not.toHaveBeenCalled()
})

test('an explicitly selected default group is used for every key in a batch', async () => {
  const { created } = renderDrawer({ defaultAuto: true })
  const user = userEvent.setup()
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Add' })).toBeEnabled()
  )
  await selectGroup(user, 'default')
  await user.type(
    screen.getByRole('textbox', { name: 'Token Name' }),
    'selected default'
  )
  fireEvent.change(screen.getByRole('spinbutton', { name: 'Quantity' }), {
    target: { value: '2' },
  })
  await user.click(screen.getByRole('button', { name: 'Add' }))
  await waitFor(() => expect(created).toHaveLength(2))
  expect(created.map((payload) => payload.group)).toEqual([
    'default',
    'default',
  ])
})

test('smart routing requires a group first and keeps that explicit manual draft', async () => {
  const { created, post } = renderDrawer()
  const user = userEvent.setup()
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Add' })).toBeEnabled()
  )
  const smart = screen.getByRole('switch', { name: 'Smart Routing' })
  expect(smart).toHaveAttribute('aria-disabled', 'true')
  await user.click(smart)
  expect(smart).not.toBeChecked()
  expect(
    screen.getByText('Select a group before enabling smart routing.')
  ).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Use my group' }))
  expect(smart).not.toHaveAttribute('aria-disabled', 'true')
  await user.click(smart)
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/token/models',
      expect.objectContaining({ group: 'default', smart_routing: true })
    )
  )
  await user.type(
    screen.getByRole('textbox', { name: 'Token Name' }),
    'smart key'
  )
  await user.click(screen.getByRole('button', { name: 'Add' }))
  await waitFor(() => expect(created).toHaveLength(1))
  expect(created[0]).toMatchObject({ group: 'default', smart_routing: true })
})

test('editing an inherited key explains and preserves its empty group', async () => {
  const { put } = renderDrawer({ currentRow: legacyKey })
  const user = userEvent.setup()
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeEnabled()
  )
  expect(screen.getByText('Using your account group (default).')).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith(
      '/api/token/',
      expect.objectContaining({ id: 7, group: '' })
    )
  )
})

test('an account without available groups cannot create a key and can refresh the empty state', async () => {
  const { server, created } = renderDrawer({ noGroups: true })
  const user = userEvent.setup()
  expect(await screen.findByText('No routing groups available')).toBeVisible()
  expect(screen.getByRole('button', { name: 'Add' })).toBeDisabled()
  server.groups = availableGroups
  await user.click(screen.getByRole('button', { name: 'Retry' }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Add' })).toBeEnabled()
  )
  expect(screen.getByRole('combobox', { name: 'Group' })).toHaveTextContent(
    'Select groups in routing order'
  )
  expect(created).toHaveLength(0)
})

test('a routing-group refresh failure blocks saving and retry preserves the unsaved draft', async () => {
  const { server, created } = renderDrawer()
  const user = userEvent.setup()
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Add' })).toBeEnabled()
  )
  await user.click(screen.getByRole('button', { name: 'Restore global Auto' }))
  await user.type(
    screen.getByRole('textbox', { name: 'Token Name' }),
    'keep this draft'
  )
  server.groupsFail = true
  await act(async () => client.refetchQueries({ queryKey: ['user-groups', 7] }))
  expect(await screen.findByText('Failed to load routing groups')).toBeVisible()
  expect(screen.getByRole('button', { name: 'Add' })).toBeDisabled()
  server.groupsFail = false
  await user.click(screen.getByRole('button', { name: 'Retry' }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Add' })).toBeEnabled()
  )
  expect(screen.getByRole('textbox', { name: 'Token Name' })).toHaveValue(
    'keep this draft'
  )
  expect(screen.getByRole('combobox', { name: 'Group' })).toHaveTextContent(
    'vip → default'
  )
  expect(created).toHaveLength(0)
})

test('rapid button and form submissions issue only one create request', async () => {
  const { server, created } = renderDrawer()
  const user = userEvent.setup()
  let resolveCreate: (() => void) | undefined
  const pending = new Promise<{
    data: { success: boolean; data: Record<string, unknown> }
  }>((resolve) => {
    resolveCreate = () => resolve({ data: { success: true, data: {} } })
  })
  server.createToken = () => pending
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Add' })).toBeEnabled()
  )
  await user.click(screen.getByRole('button', { name: 'Restore global Auto' }))
  await user.type(
    screen.getByRole('textbox', { name: 'Token Name' }),
    'single request'
  )
  const form = screen
    .getByRole('textbox', { name: 'Token Name' })
    .closest('form')
  if (!form) throw new Error('API key form is missing')
  try {
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Add' }))
      fireEvent.submit(form)
    })
    await waitFor(() => expect(created).toHaveLength(1))
    expect(screen.getByRole('button', { name: 'Saving...' })).toBeDisabled()
  } finally {
    await act(async () => resolveCreate?.())
  }
})

test('a network error during batch creation reports confirmed keys and stops before submitting the rest', async () => {
  const { server, created, onCreated, close } = renderDrawer()
  const user = userEvent.setup()
  server.createToken = async (_data, attempt) => {
    if (attempt === 2) throw new Error('Connection lost during creation')
    return { data: { success: true, data: {} } }
  }
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Add' })).toBeEnabled()
  )
  await user.click(screen.getByRole('button', { name: 'Restore global Auto' }))
  await user.type(
    screen.getByRole('textbox', { name: 'Token Name' }),
    'partial batch'
  )
  fireEvent.change(screen.getByRole('spinbutton', { name: 'Quantity' }), {
    target: { value: '3' },
  })
  await user.click(screen.getByRole('button', { name: 'Add' }))
  expect(
    await screen.findByText(
      'Creation confirmed for 1 of 3 API keys. Check the key list before trying again.'
    )
  ).toBeVisible()
  expect(created).toHaveLength(2)
  expect(onCreated).toHaveBeenCalledTimes(1)
  expect(close).toHaveBeenCalledWith(false)
})
