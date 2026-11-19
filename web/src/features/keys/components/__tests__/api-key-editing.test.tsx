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
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import type { ApiKey } from '../../types'
import { ApiKeysDialogs } from '../api-keys-dialogs'
import { ApiKeysMutateDrawer } from '../api-keys-mutate-drawer'
import { ApiKeysProvider, useApiKeys } from '../api-keys-provider'

const i18n = createInstance()
await i18n.init({
  lng: 'en',
  resources: { en: { translation: {} } },
  initAsync: false,
})

const finiteKey: ApiKey = {
  id: 303,
  name: 'editing fixture',
  key: 'masked-fixture',
  status: 1,
  remain_quota: 1_000_000,
  used_quota: 500_000,
  unlimited_quota: false,
  expired_time: -1,
  created_time: 1,
  accessed_time: 0,
  group: 'paid',
  auto_groups: [],
  cross_group_retry: false,
  model_limits_enabled: false,
  model_limits: '',
  allow_ips: '',
  allow_user_quota_query: true,
}

let client: QueryClient

function EditLauncher(props: { apiKey: ApiKey }) {
  const keys = useApiKeys()
  return (
    <button
      type='button'
      onClick={() => {
        keys.setCurrentRow(props.apiKey)
        keys.setOpen('update')
      }}
    >
      Edit saved key
    </button>
  )
}

async function renderEdit(key: ApiKey, useDialogs = false) {
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/user/self/groups') {
      return {
        data: {
          success: true,
          data: { paid: { desc: 'Paid group', ratio: 1 } },
        },
      }
    }
    if (url === '/api/token/auto-groups') {
      return {
        data: { success: true, data: { groups: ['paid'], max_count: 5 } },
      }
    }
    if (url === '/api/token/303') return { data: { success: true, data: key } }
    throw new Error(`Unexpected GET ${url}`)
  })
  vi.spyOn(api, 'post').mockImplementation(async (url) => {
    expect(url).toBe('/api/token/models')
    return { data: { success: true, data: ['fixture-model'] } }
  })
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  render(
    <QueryClientProvider client={client}>
      <I18nextProvider i18n={i18n}>
        <ApiKeysProvider>
          {useDialogs ? (
            <>
              <EditLauncher apiKey={key} />
              <ApiKeysDialogs />
            </>
          ) : (
            <ApiKeysMutateDrawer open currentRow={key} onOpenChange={vi.fn()} />
          )}
        </ApiKeysProvider>
      </I18nextProvider>
    </QueryClientProvider>
  )
  if (useDialogs) {
    await userEvent.click(
      screen.getByRole('button', { name: 'Edit saved key' })
    )
  }
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeEnabled()
  )
  return put
}

beforeEach(() => {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  localStorage.clear()
  localStorage.setItem('quota_per_unit', '500000')
  useAuthStore
    .getState()
    .auth.setUser({ id: 7, username: 'fixture', role: 1, group: 'paid' })
})

test('editing a name keeps unavailable manual groups until they are explicitly removed', async () => {
  const groups = ['retired', 'paid', 'legacy']
  const put = await renderEdit({
    ...finiteKey,
    group: 'auto',
    auto_groups: groups,
    smart_routing: false,
  })
  put.mockResolvedValueOnce({
    data: {
      success: false,
      message: 'Auto group retired is unavailable or unauthorized',
    },
  })
  const user = userEvent.setup()
  const name = screen.getByRole('textbox', { name: 'Token Name' })
  await user.clear(name)
  await user.type(name, 'renamed key')
  expect(screen.getByRole('combobox', { name: 'Group' })).toHaveTextContent(
    'retired → paid → legacy'
  )
  expect(screen.getAllByText('Unavailable')).toHaveLength(2)
  await user.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() => expect(put).toHaveBeenCalledOnce())
  expect(put.mock.calls[0][1]).toMatchObject({
    name: 'renamed key',
    group: 'auto',
    auto_groups: groups,
  })
  await user.click(screen.getByRole('button', { name: 'Remove retired' }))
  await user.click(screen.getByRole('button', { name: 'Remove legacy' }))
  await user.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() => expect(put).toHaveBeenCalledTimes(2))
  expect(put.mock.calls[1][1]).toMatchObject({
    group: 'paid',
    auto_groups: [],
  })
})

test('an older save cannot close the same key after its editor has been reopened', async () => {
  const put = await renderEdit(finiteKey, true)
  let resolveSave: (() => void) | undefined
  const pending = new Promise<{ data: { success: boolean } }>((resolve) => {
    resolveSave = () => resolve({ data: { success: true } })
  })
  put.mockReturnValueOnce(pending)
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() => expect(put).toHaveBeenCalledOnce())
  const closeButton = screen.getAllByRole('button', { name: 'Close' }).at(-1)
  if (!closeButton) throw new Error('Edit drawer close button missing')
  await user.click(closeButton)
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  await user.click(screen.getByRole('button', { name: 'Edit saved key' }))
  expect(await screen.findByRole('dialog')).toBeVisible()
  await act(async () => {
    resolveSave?.()
    await pending
  })
  expect(screen.getByRole('dialog')).toBeVisible()
  expect(screen.getByRole('button', { name: 'Save changes' })).toBeEnabled()
})

afterEach(async () => {
  cleanup()
  await client.cancelQueries()
  client.clear()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
  localStorage.clear()
})

test('renaming a finite key omits its stale remaining quota from the update', async () => {
  const put = await renderEdit(finiteKey)
  const user = userEvent.setup()
  await user.type(
    screen.getByRole('textbox', { name: 'Token Name' }),
    ' renamed'
  )
  await user.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() => expect(put).toHaveBeenCalledOnce())
  expect(put.mock.calls[0][1]).toMatchObject({
    id: 303,
    name: 'editing fixture renamed',
  })
  expect(put.mock.calls[0][1]).not.toHaveProperty('remain_quota')
})

test('an explicit finite quota edit submits the requested amount', async () => {
  const put = await renderEdit(finiteKey)
  fireEvent.change(screen.getByRole('spinbutton', { name: /^Quota/ }), {
    target: { value: '2.5' },
  })
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() => expect(put).toHaveBeenCalledOnce())
  expect(put.mock.calls[0][1]).toMatchObject({
    remain_quota: 1_250_000,
    unlimited_quota: false,
  })
})

test('switching an unlimited key to a finite zero quota sends an explicit zero', async () => {
  const put = await renderEdit({ ...finiteKey, unlimited_quota: true })
  const user = userEvent.setup()
  await user.click(screen.getByRole('switch', { name: 'Unlimited Quota' }))
  await user.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() => expect(put).toHaveBeenCalledOnce())
  expect(put.mock.calls[0][1]).toMatchObject({
    remain_quota: 0,
    unlimited_quota: false,
  })
})

test('renaming a key with an enabled empty allowlist keeps all models denied', async () => {
  const put = await renderEdit({ ...finiteKey, model_limits_enabled: true })
  const user = userEvent.setup()
  expect(
    screen.getByRole('switch', { name: 'Model limits enabled' })
  ).toBeChecked()
  expect(
    screen.getByText(
      'Model restrictions are enabled with an empty allowlist. This key cannot use any model.'
    )
  ).toBeVisible()
  await user.type(
    screen.getByRole('textbox', { name: 'Token Name' }),
    ' renamed'
  )
  await user.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() => expect(put).toHaveBeenCalledOnce())
  expect(put.mock.calls[0][1]).toMatchObject({
    model_limits_enabled: true,
    model_limits: '',
  })
})

test('a disabled model restriction keeps its dormant allowlist through an unrelated edit', async () => {
  const put = await renderEdit({ ...finiteKey, model_limits: 'fixture-model' })
  const user = userEvent.setup()
  expect(
    screen.getByRole('switch', { name: 'Model limits enabled' })
  ).not.toBeChecked()
  expect(screen.getByRole('combobox', { name: 'Select models' })).toBeDisabled()
  await user.type(
    screen.getByRole('textbox', { name: 'Token Name' }),
    ' renamed'
  )
  await user.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() => expect(put).toHaveBeenCalledOnce())
  expect(put.mock.calls[0][1]).toMatchObject({
    model_limits_enabled: false,
    model_limits: 'fixture-model',
  })
})

test('explicitly disabling model restrictions preserves the allowlist for re-enabling', async () => {
  const put = await renderEdit({
    ...finiteKey,
    model_limits_enabled: true,
    model_limits: 'fixture-model',
  })
  const user = userEvent.setup()
  await user.click(screen.getByRole('switch', { name: 'Model limits enabled' }))
  expect(screen.getByRole('combobox', { name: 'Select models' })).toBeDisabled()
  await user.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() => expect(put).toHaveBeenCalledOnce())
  expect(put.mock.calls[0][1]).toMatchObject({
    model_limits_enabled: false,
    model_limits: 'fixture-model',
  })
})

test('uses the version of the open edit form, not a later background detail refresh', async () => {
  const put = await renderEdit({ ...finiteKey, settings_version: 17 })
  await act(async () => {
    client.setQueriesData({ queryKey: ['api-key'] }, () => ({
      success: true,
      data: { ...finiteKey, settings_version: 18 },
    }))
  })
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith(
      '/api/token/',
      expect.objectContaining({
        id: finiteKey.id,
        expected_version: 17,
      })
    )
  )
})
