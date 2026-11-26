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
  within,
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
import type { KeyGroupPreviewDetails } from '../key-group-cards'

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
  previewDetails,
}: {
  entry?: ApiKey
  draft?: MarketKeyDraft
  groups?: Record<string, unknown>
  previewDetails?: KeyGroupPreviewDetails
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
            previewDetails={previewDetails}
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

async function pickGroup(
  user: ReturnType<typeof userEvent.setup>,
  name: string
) {
  await user.click(await screen.findByRole('button', { name }))
  await user.click(
    within(screen.getByRole('region', { name: 'Group preview' })).getByRole(
      'button',
      { name: 'Set as primary channel' }
    )
  )
}

test('creates a fixed Key only after confirming one authorized preview and explicitly submitting', async () => {
  const { user, post } = openEditor()
  await user.type(await screen.findByLabelText('Token Name'), 'New key')
  const save = screen.getByRole('button', { name: 'Create' })
  expect(save).toBeDisabled()
  expect(screen.queryByText('Legacy Auto')).not.toBeInTheDocument()
  expect(screen.queryByText('Smart Routing')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Group One' }))
  expect(save).toBeDisabled()
  expect(post).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Other 1' }))
  expect(
    screen.queryByRole('button', { name: 'Group One' })
  ).not.toBeInTheDocument()
  await pickGroup(user, 'Group Two')
  await user.click(screen.getByRole('button', { name: 'All 2' }))
  expect(screen.getByText('Selected: Group Two')).toBeVisible()
  expect(screen.queryByText('Selected: Group One')).not.toBeInTheDocument()
  const filter = screen.getByRole('textbox', {
    name: 'Search by group name, description, or platform',
  })
  await user.click(filter)
  await user.keyboard('{Enter}')
  expect(post).not.toHaveBeenCalled()
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
    allow_user_quota_query: true,
  })
})

test('keeps create and cancel outside the scrollable fields when advanced settings open', async () => {
  const { user, post, close } = openEditor()
  const dialog = screen.getByRole('dialog', { name: 'Create API Key' })
  const nameField = await within(dialog).findByRole('textbox', {
    name: 'Token Name',
  })
  const body = nameField.closest('[data-slot="api-key-form-body"]')
  const create = within(dialog).getByRole('button', { name: 'Create' })
  const cancel = within(dialog).getByRole('button', { name: 'Cancel' })
  const advanced = within(dialog).getByRole('button', {
    name: 'Advanced Settings',
  })

  expect(body).toHaveClass('min-h-0', 'overflow-y-auto')
  expect(dialog).toHaveClass('overflow-hidden')
  expect(body).not.toContainElement(create)
  expect(body).not.toContainElement(cancel)
  const footer = create.closest('[data-slot="dialog-footer"]')
  expect(footer).toHaveClass('shrink-0')
  expect(footer).toContainElement(cancel)
  expect(advanced).toHaveAttribute('aria-expanded', 'false')
  await user.click(advanced)
  expect(body).toContainElement(
    within(dialog).getByLabelText('IP Whitelist (supports CIDR)')
  )
  expect(create).toBeVisible()
  expect(create).toBeDisabled()
  await user.click(cancel)
  expect(close).toHaveBeenCalledWith(false)
  expect(post).not.toHaveBeenCalled()
})

test('keyboard previews without choosing and announces a spaced group’s whole description and explicit zero rate', async () => {
  const description =
    'Authorized testing group with a longer custom description'
  const { user, post } = openEditor({
    groups: {
      ...options,
      'team test': {
        ratio: 0,
        desc: description,
        catalog: { label: 'Team Test', exclusive: true },
      },
    },
    previewDetails: ({ groupId }) => <p>Preview fixture: {groupId}</p>,
  })
  const one = await screen.findByRole('button', { name: 'Group One' })
  const free = screen.getByRole('button', { name: 'Team Test' })
  expect(free).toHaveAccessibleDescription(`${description} 0× Exclusive`)
  expect(
    screen.getByRole('button', { name: 'OpenAI 1' }).querySelector('svg')
  ).toBeInTheDocument()
  expect(screen.queryByText(/Preview fixture/)).not.toBeInTheDocument()
  one.focus()
  await user.keyboard('{Enter}')
  const preview = screen.getByRole('region', { name: 'Group preview' })
  expect(within(preview).getByText('Preview fixture: a')).toBeVisible()
  expect(one).toHaveAttribute('aria-pressed', 'true')
  expect(screen.queryByText('Selected: Group One')).not.toBeInTheDocument()
  const choose = within(preview).getByRole('button', {
    name: 'Set as primary channel',
  })
  choose.focus()
  await user.keyboard('[Space]')
  expect(screen.getByText('Selected: Group One')).toBeVisible()
  expect(choose).toBeDisabled()
  expect(post).not.toHaveBeenCalled()
})

test('searches group descriptions and platform and sorts real multipliers both ways', async () => {
  const { user, post } = openEditor()
  const search = await screen.findByRole('textbox', {
    name: 'Search by group name, description, or platform',
  })
  const groups = within(screen.getByRole('group', { name: 'Groups' }))
  const sort = screen.getByRole('combobox', { name: 'Sort groups' })
  await user.type(search, 'openai')
  expect(groups.getByRole('button', { name: 'Group One' })).toBeVisible()
  expect(
    groups.queryByRole('button', { name: 'Group Two' })
  ).not.toBeInTheDocument()
  await user.clear(search)
  await user.type(search, 'restricted')
  expect(groups.getByRole('button', { name: 'Group Two' })).toBeVisible()
  await user.clear(search)
  await user.selectOptions(sort, 'ascending')
  expect(
    groups
      .getAllByRole('button')
      .map((item) => item.getAttribute('aria-pressed'))
  ).toHaveLength(2)
  expect(groups.getAllByRole('button')[0]).toHaveAccessibleName('Group Two')
  await user.selectOptions(sort, 'descending')
  expect(groups.getAllByRole('button')[0]).toHaveAccessibleName('Group One')
  expect(post).not.toHaveBeenCalled()
})

test('opens and closes the narrow inspector without mounting hidden health or committing a group', async () => {
  const original = window.matchMedia
  vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
    ...original(query),
    matches: query === '(max-width: 1023px)' || original(query).matches,
  }))
  const { user, post } = openEditor({
    previewDetails: ({ groupId }) => <p>Preview fixture: {groupId}</p>,
  })
  const group = await screen.findByRole('button', { name: 'Group One' })
  await user.click(group)
  const title = screen.getByRole('heading', { name: 'Group preview' })
  await waitFor(() => expect(title).toHaveFocus())
  expect(screen.getByText('Preview fixture: a')).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Back to group list' }))
  await waitFor(() => expect(group).toHaveFocus())
  expect(screen.queryByText('Preview fixture: a')).not.toBeInTheDocument()
  expect(screen.queryByText('Selected: Group One')).not.toBeInTheDocument()
  expect(post).not.toHaveBeenCalled()
})

test('exposes an initially enabled create quota-query permission; explicit false is sent without affecting limits', async () => {
  const { user, post } = openEditor()
  await user.type(await screen.findByLabelText('Token Name'), 'Quota query off')
  await pickGroup(user, 'Group One')
  await user.click(screen.getByRole('button', { name: 'Advanced Settings' }))
  const setting = screen.getByRole('switch', {
    name: 'Allow querying user quota',
  })
  expect(setting).toBeChecked()
  await user.click(setting)
  await user.click(screen.getByRole('button', { name: 'Create' }))
  expect(post).toHaveBeenLastCalledWith(
    '/api/token/',
    expect.objectContaining({
      group: 'a',
      allow_user_quota_query: false,
      remain_quota: 0,
      unlimited_quota: true,
      expired_time: -1,
    })
  )
})

test('editing a saved disabled quota-query permission updates only that option when changed', async () => {
  const { user, put } = openEditor({
    entry: { ...saved, allow_user_quota_query: false },
  })
  await screen.findByLabelText('Token Name')
  await user.click(screen.getByRole('button', { name: 'Advanced Settings' }))
  const setting = screen.getByRole('switch', {
    name: 'Allow querying user quota',
  })
  expect(setting).not.toBeChecked()
  await user.click(setting)
  await user.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(put).toHaveBeenCalledExactlyOnceWith('/api/token/', {
    id: 7,
    expected_version: 17,
    allow_user_quota_query: true,
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
  expect(screen.queryByText('Selected: Group One')).not.toBeInTheDocument()
  await pickGroup(user, 'Group One')
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
  expect(screen.getByText('Selected: Group Two')).toBeVisible()
  expect(post).toHaveBeenCalledWith('/api/token/models', { group: 'b' })
  expect(post).toHaveBeenCalledTimes(1)
  await user.type(name, 'Generated draft')
  await user.click(screen.getByRole('button', { name: 'Create' }))
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
  await pickGroup(user, 'Group One')
  expect(
    await screen.findByText(
      /The group or model from the catalogue is unavailable/
    )
  ).toBeVisible()
  expect(screen.getByRole('button', { name: 'Create' })).toBeDisabled()
  await pickGroup(user, 'Group Two')
  await waitFor(() =>
    expect(
      screen.getByRole('switch', { name: 'Model limits enabled' })
    ).toBeChecked()
  )
  expect(post.mock.calls).toEqual([
    ['/api/token/models', { group: 'a' }],
    ['/api/token/models', { group: 'b' }],
  ])
  await user.click(screen.getByRole('button', { name: 'Create' }))
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
  expect(screen.getByRole('button', { name: 'Create' })).toBeDisabled()
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
  await pickGroup(user, 'Group One')
  const submit = screen.getByRole('button', { name: 'Create' })
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
  await pickGroup(user, 'Group One')
  await user.click(screen.getByRole('button', { name: 'Create' }))
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
  await pickGroup(user, 'Group Two')
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
  expect(screen.getByRole('button', { name: 'Create' })).toBeDisabled()
  expect(
    screen.getByRole('switch', { name: 'Model limits enabled' })
  ).not.toBeChecked()
  expect(post).toHaveBeenCalledExactlyOnceWith('/api/token/models', {
    group: 'a',
  })
  await user.click(
    screen.getByRole('button', { name: 'Discard catalogue draft' })
  )
  expect(screen.getByRole('button', { name: 'Create' })).toBeEnabled()
})

test('validates new IP restrictions and sends the normalized line-delimited format', async () => {
  const { user, post } = openEditor()
  await user.type(await screen.findByLabelText('Token Name'), 'Restricted')
  await pickGroup(user, 'Group One')
  await user.click(screen.getByRole('button', { name: 'Advanced Settings' }))
  const ip = screen.getByLabelText('IP Whitelist (supports CIDR)')
  await user.type(ip, ',')
  expect(screen.getByRole('button', { name: 'Create' })).toBeDisabled()
  await user.clear(ip)
  await user.type(ip, '192.0.2.1, 2001:db8::/32')
  await user.click(screen.getByRole('button', { name: 'Create' }))
  expect(post).toHaveBeenCalledWith(
    '/api/token/',
    expect.objectContaining({
      allow_ips: '192.0.2.1\n2001:db8::/32',
    })
  )
})
