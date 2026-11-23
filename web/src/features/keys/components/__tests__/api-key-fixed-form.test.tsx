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

import type { MarketKeyDraft } from '../../lib/market-key-draft'
import { apiKeySchema, type ApiKey } from '../../types'
import { ApiKeysMutateDrawer } from '../api-keys-mutate-drawer'
import { ApiKeysProvider } from '../api-keys-provider'

const i18n = createInstance()
await i18n.init({
  lng: 'en',
  resources: { en: { translation: {} } },
  initAsync: false,
})
let client: QueryClient

const saved = apiKeySchema.parse({
  id: 7,
  name: 'Old key',
  key: 'masked-credential',
  settings_version: 17,
  status: 1,
  remain_quota: 1_000_000,
  used_quota: 2_000_000,
  unlimited_quota: false,
  expired_time: 1_900_000_000,
  created_time: 1,
  accessed_time: 1,
  group: 'a',
  model_limits_enabled: false,
})

const options = {
  a: {
    ratio: 1,
    desc: 'Default',
    catalog: { label: 'Group One', platform: 'openai' },
  },
  b: {
    ratio: 0.5,
    desc: 'Restricted tier',
    catalog: { label: 'Group Two', platform: 'anthropic', exclusive: true },
  },
  auto: { ratio: 'auto', desc: 'Legacy Auto' },
}

function openEditor({
  entry,
  draft,
  groups = options,
}: {
  entry?: ApiKey
  draft?: MarketKeyDraft
  groups?: Record<string, unknown>
} = {}) {
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/user/self/groups') {
      return { data: { success: true, data: groups } }
    }
    if (url === '/api/token/7') return { data: { success: true, data: entry } }
    if (url === '/api/token/7/models') {
      return { data: { success: true, data: ['a-model'] } }
    }
    throw new Error(`Unexpected GET: ${url}`)
  })
  const post = vi.spyOn(api, 'post').mockImplementation(async (url, input) => {
    if (url === '/api/token/models') {
      const models =
        (input as { group: string }).group === 'a' ? ['a-model'] : ['b-model']
      return { data: { success: true, data: models } }
    }
    if (url !== '/api/token/') throw new Error(`Unexpected POST: ${url}`)
    return { data: { success: true } }
  })
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  const close = vi.fn()
  render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={client}>
        <ApiKeysProvider>
          <ApiKeysMutateDrawer
            open
            currentRow={entry}
            draft={draft}
            onOpenChange={close}
          />
        </ApiKeysProvider>
      </QueryClientProvider>
    </I18nextProvider>
  )
  return { post, get, put, close, user: userEvent.setup() }
}

beforeEach(() => {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
})
afterEach(() => {
  cleanup()
  client.clear()
  vi.restoreAllMocks()
})

test('creates a fixed key only after selecting one authorized radio card', async () => {
  const { user, post } = openEditor()
  await user.type(await screen.findByLabelText('Token Name'), 'New key')
  const save = screen.getByRole('button', { name: 'Add' })
  expect(save).toBeDisabled()
  expect(screen.queryByText('Legacy Auto')).not.toBeInTheDocument()
  expect(screen.queryByText('Smart Routing')).not.toBeInTheDocument()
  await user.click(screen.getByRole('radio', { name: /Group One/ }))
  await user.click(screen.getByRole('button', { name: 'Other' }))
  expect(
    screen.queryByRole('radio', { name: /Group One/ })
  ).not.toBeInTheDocument()
  expect(screen.getByText('Selected: Group One')).toBeVisible()
  await user.click(screen.getByRole('radio', { name: /Group Two/ }))
  await user.click(screen.getByRole('button', { name: 'All' }))
  expect(screen.getByRole('radio', { name: /Group One/ })).not.toBeChecked()
  expect(screen.getByRole('radio', { name: /Group Two/ })).toBeChecked()
  await user.click(save)
  expect(post).toHaveBeenCalledTimes(1)
  expect(post).toHaveBeenCalledWith('/api/token/', {
    group: 'b',
    name: 'New key',
    remain_quota: 0,
    unlimited_quota: true,
    expired_time: -1,
    model_limits_enabled: false,
    model_limits: '',
    allow_ips: '',
  })
})

test('does not rewrite expiry, remaining quota, dormant restrictions or legacy settings on rename', async () => {
  const { user, put } = openEditor({
    entry: {
      ...saved,
      group: 'auto',
      auto_groups: ['removed', 'a'],
      model_limits: 'a-model',
      model_limits_enabled: false,
      allow_user_quota_query: false,
      cross_group_retry: true,
    },
  })
  expect(await screen.findByText(/This key uses legacy routing/)).toBeVisible()
  await user.type(await screen.findByLabelText('Token Name'), ' renamed')
  await user.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(put).toHaveBeenCalledWith('/api/token/', {
    id: 7,
    expected_version: 17,
    name: 'Old key renamed',
  })
})

test('can explicitly adopt the same concrete group once used by a legacy smart key', async () => {
  const { user, put } = openEditor({ entry: { ...saved, smart_routing: true } })
  const radio = await screen.findByRole('radio', { name: /Group One/ })
  expect(radio).not.toBeChecked()
  await user.click(radio)
  await user.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(put).toHaveBeenCalledWith('/api/token/', {
    id: 7,
    expected_version: 17,
    group: 'a',
  })
})

test('does not use a newer fetched version for the already open edit', async () => {
  const { user, put } = openEditor({ entry: saved })
  const name = await screen.findByLabelText('Token Name')
  await act(async () => {
    client.setQueryData(['api-key', null, 7], {
      success: true,
      data: { ...saved, settings_version: 18 },
    })
  })
  await user.type(name, ' changed')
  await user.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(put).toHaveBeenCalledWith('/api/token/', {
    id: 7,
    expected_version: 17,
    name: 'Old key changed',
  })
})

test('validates a catalogue draft against current group and model scope; never submits automatically', async () => {
  const { user, post } = openEditor({ draft: { group: 'b', model: 'b-model' } })
  const name = await screen.findByLabelText('Token Name')
  await waitFor(() =>
    expect(
      screen.getByRole('switch', { name: 'Model limits enabled' })
    ).toBeChecked()
  )
  expect(screen.getByRole('radio', { name: /Group Two/ })).toBeChecked()
  expect(post).toHaveBeenCalledWith('/api/token/models', { group: 'b' })
  expect(post).toHaveBeenCalledTimes(1)
  await user.type(name, 'Generated draft')
  await user.click(screen.getByRole('button', { name: 'Add' }))
  expect(post).toHaveBeenLastCalledWith(
    '/api/token/',
    expect.objectContaining({
      group: 'b',
      model_limits_enabled: true,
      model_limits: 'b-model',
    })
  )
})

test('a model-only catalogue draft waits for group selection and rechecks another authorized group', async () => {
  const { user, post } = openEditor({ draft: { model: 'b-model' } })
  await user.type(await screen.findByLabelText('Token Name'), 'Model first')
  expect(
    screen.queryByText(/The group or model from the catalogue is unavailable/)
  ).not.toBeInTheDocument()
  expect(post).not.toHaveBeenCalled()
  await user.click(screen.getByRole('radio', { name: /Group One/ }))
  expect(
    await screen.findByText(
      /The group or model from the catalogue is unavailable/
    )
  ).toBeVisible()
  expect(screen.getByRole('button', { name: 'Add' })).toBeDisabled()
  await user.click(screen.getByRole('radio', { name: /Group Two/ }))
  await waitFor(() =>
    expect(
      screen.getByRole('switch', { name: 'Model limits enabled' })
    ).toBeChecked()
  )
  expect(post.mock.calls).toEqual([
    ['/api/token/models', { group: 'a' }],
    ['/api/token/models', { group: 'b' }],
  ])
  await user.click(screen.getByRole('button', { name: 'Add' }))
  expect(post).toHaveBeenLastCalledWith(
    '/api/token/',
    expect.objectContaining({ group: 'b', model_limits: 'b-model' })
  )
})

test('never previews or selects a catalogue group the key owner cannot use', async () => {
  const { user, post } = openEditor({
    draft: { group: 'other-private', model: 'private-model' },
  })
  await user.type(await screen.findByLabelText('Token Name'), 'Blocked')
  expect(
    screen.getByText(/The group or model from the catalogue is unavailable/)
  ).toBeVisible()
  expect(screen.getByRole('button', { name: 'Add' })).toBeDisabled()
  expect(post).not.toHaveBeenCalled()
})

test('does not double-submit when a request is pending', async () => {
  const { user, post } = openEditor()
  let finish: () => void = () => undefined
  post.mockImplementationOnce(async () => {
    await new Promise<void>((resolve) => {
      finish = resolve
    })
    return { data: { success: true } }
  })
  await user.type(await screen.findByLabelText('Token Name'), 'New key')
  await user.click(screen.getByRole('radio', { name: /Group One/ }))
  const submit = screen.getByRole('button', { name: 'Add' })
  const form = submit.closest('form')
  if (!form) throw new Error('Expected the API key form')
  fireEvent.submit(form)
  fireEvent.submit(form)
  expect(post).toHaveBeenCalledTimes(1)
  await act(async () => finish())
})

test('recovered error leaves an unknown creation outcome for manual checking without replay', async () => {
  const { user, post, close } = openEditor()
  post.mockRejectedValueOnce(new Error('network interrupted'))
  await user.type(await screen.findByLabelText('Token Name'), 'New key')
  await user.click(screen.getByRole('radio', { name: /Group One/ }))
  await user.click(screen.getByRole('button', { name: 'Add' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Could not confirm key creation'
  )
  expect(post).toHaveBeenCalledTimes(1)
  expect(close).not.toHaveBeenCalled()
})

test('keeps a restricted whitelist when the selected group changes and the saved model is unavailable', async () => {
  const { user, post, put } = openEditor({
    entry: { ...saved, model_limits_enabled: true, model_limits: 'a-model' },
  })
  await user.click(await screen.findByRole('radio', { name: /Group Two/ }))
  await waitFor(() =>
    expect(post).toHaveBeenLastCalledWith('/api/token/models', { group: 'b' })
  )
  expect(
    screen.getByRole('switch', { name: 'Model limits enabled' })
  ).toBeChecked()
  await user.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(put).toHaveBeenLastCalledWith('/api/token/', {
    id: 7,
    expected_version: 17,
    group: 'b',
  })
})

test('a finite zero quota remains an explicit limit and blank validity means never', async () => {
  const { user, put } = openEditor({ entry: saved })
  const quota = await screen.findByLabelText('Remaining quota (USD)')
  await user.clear(quota)
  await user.type(quota, '0')
  await user.clear(screen.getByLabelText('Validity (days)'))
  await user.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(put).toHaveBeenCalledWith('/api/token/', {
    id: 7,
    expected_version: 17,
    remain_quota: 0,
    unlimited_quota: false,
    expired_time: -1,
  })
})

test('an unauthorized model inside an authorized group is discarded without widening a draft', async () => {
  const { user, post } = openEditor({ draft: { group: 'a', model: 'b-model' } })
  await user.type(
    await screen.findByLabelText('Token Name'),
    'Unavailable model'
  )
  expect(
    await screen.findByText(
      /The group or model from the catalogue is unavailable/
    )
  ).toBeVisible()
  expect(screen.getByRole('button', { name: 'Add' })).toBeDisabled()
  expect(
    screen.getByRole('switch', { name: 'Model limits enabled' })
  ).not.toBeChecked()
  expect(post).toHaveBeenCalledExactlyOnceWith('/api/token/models', {
    group: 'a',
  })
  await user.click(
    screen.getByRole('button', { name: 'Discard catalogue draft' })
  )
  expect(screen.getByRole('button', { name: 'Add' })).toBeEnabled()
})

test('validates new IP restrictions and sends the normalized line-delimited format', async () => {
  const { user, post } = openEditor()
  await user.type(await screen.findByLabelText('Token Name'), 'Restricted')
  await user.click(screen.getByRole('radio', { name: /Group One/ }))
  await user.click(screen.getByRole('button', { name: 'Advanced Settings' }))
  const ip = screen.getByLabelText('IP Whitelist (supports CIDR)')
  await user.type(ip, ',')
  expect(screen.getByRole('button', { name: 'Add' })).toBeDisabled()
  await user.clear(ip)
  await user.type(ip, '192.0.2.1, 2001:db8::/32')
  await user.click(screen.getByRole('button', { name: 'Add' }))
  expect(post).toHaveBeenCalledWith(
    '/api/token/',
    expect.objectContaining({
      allow_ips: '192.0.2.1\n2001:db8::/32',
    })
  )
})
