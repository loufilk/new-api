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
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { useForm } from 'react-hook-form'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { Form } from '@/components/ui/form'
import { useAuthStore } from '@/stores/auth-store'

import {
  getApiKeyFormDefaultValues,
  type ApiKeyFormValues,
} from '../../lib/api-key-form'
import { useKeyGroupPresetStore } from '../../stores/group-presets'
import { ApiKeyGroupHealth } from '../api-key-group-health'
import { ApiKeyRoutingFields } from '../api-key-routing-fields'

const i18n = createInstance()
await i18n.init({
  lng: 'en',
  resources: { en: { translation: {} } },
  initAsync: false,
})
const groups = [
  { value: 'default', label: 'default', ratio: 1 },
  { value: 'vip', label: 'vip', ratio: 2 },
  { value: 'fast', label: 'fast', ratio: 3 },
]
const submitted = vi.fn()

function RoutingForm(props: {
  defaults?: Partial<ApiKeyFormValues>
  allowAuto?: boolean
  maxCount?: number
  globalGroups?: string[]
}) {
  const form = useForm<ApiKeyFormValues>({
    defaultValues: { ...getApiKeyFormDefaultValues(true), ...props.defaults },
  })
  return (
    <I18nextProvider i18n={i18n}>
      <Form {...form}>
        <form onSubmit={form.handleSubmit(submitted)}>
          <ApiKeyRoutingFields
            options={groups}
            globalGroups={props.globalGroups ?? ['vip', 'default']}
            maxCount={props.maxCount ?? 3}
            allowAuto={props.allowAuto ?? true}
          />
          <button type='submit'>Submit key</button>
        </form>
      </Form>
    </I18nextProvider>
  )
}

beforeEach(() => {
  localStorage.clear()
  useAuthStore
    .getState()
    .auth.setUser({ id: 7, username: 'fixture', role: 1, group: 'default' })
  useKeyGroupPresetStore.setState({ ownerId: null, presets: [] })
  submitted.mockClear()
})
afterEach(() => {
  cleanup()
  useAuthStore.getState().auth.reset()
  localStorage.clear()
})

describe('API key routing controls', () => {
  it('disables manual groups when smart routing is on and restores the exact manual order when off', async () => {
    const user = userEvent.setup()
    render(
      <RoutingForm
        defaults={{
          group: 'auto',
          auto_groups_mode: 'custom',
          auto_groups: ['fast', 'vip'],
        }}
      />
    )
    const picker = screen.getByRole('combobox', { name: 'Group' })
    expect(picker).toHaveTextContent('fast → vip')
    await user.click(screen.getByRole('switch', { name: 'Smart Routing' }))
    expect(picker).toBeDisabled()
    expect(
      screen.getByRole('button', { name: 'Save as preset' })
    ).toBeDisabled()
    expect(screen.getAllByRole('radio')).toHaveLength(4)
    expect(
      screen.getByRole('radio', { name: 'Smart Auto (best overall)' })
    ).toBeChecked()
    await user.click(
      screen.getByRole('radio', { name: 'Price first (lowest cost)' })
    )
    expect(
      screen.getByRole('radio', { name: 'Price first (lowest cost)' })
    ).toBeChecked()
    expect(
      screen.getByRole('radio', { name: 'Smart Auto (best overall)' })
    ).not.toBeChecked()
    await user.click(screen.getByRole('switch', { name: 'Smart Routing' }))
    expect(picker).toBeEnabled()
    expect(picker).toHaveTextContent('fast → vip')
    expect(screen.queryByRole('radio')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Submit key' }))
    await waitFor(() => expect(submitted).toHaveBeenCalled())
    expect(submitted.mock.calls[0][0]).toMatchObject({
      smart_routing: false,
      routing_strategy: 'price',
      auto_groups: ['fast', 'vip'],
    })
  })

  it('keeps cross-group retries possible when the one-channel-per-group switch is enabled', async () => {
    const user = userEvent.setup()
    render(<RoutingForm />)
    expect(
      screen.getByRole('switch', { name: 'Cross-group retry' })
    ).toBeVisible()
    await user.click(
      screen.getByRole('switch', { name: 'No retries within a group' })
    )
    expect(
      screen.queryByRole('switch', { name: 'Cross-group retry' })
    ).not.toBeInTheDocument()
    expect(
      screen.getByText(/A safe retry may continue to another available group/)
    ).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Submit key' }))
    await waitFor(() => expect(submitted).toHaveBeenCalled())
    expect(submitted.mock.calls[0][0]).toMatchObject({
      no_retry_within_group: true,
      cross_group_retry: true,
    })
  })

  it('searches groups and retains selection sequence rather than sorting by name', async () => {
    const user = userEvent.setup()
    render(
      <RoutingForm
        defaults={{
          group: 'auto',
          auto_groups_mode: 'custom',
          auto_groups: [],
        }}
      />
    )
    await user.click(screen.getByRole('combobox', { name: 'Group' }))
    const search = screen.getByPlaceholderText('Search group name')
    await user.type(search, 'fast')
    await user.click(screen.getByRole('option', { name: /^fast/ }))
    await user.clear(search)
    await user.type(search, 'vip')
    await user.click(screen.getByRole('option', { name: /^vip/ }))
    await user.keyboard('{Escape}')
    expect(screen.getByRole('combobox', { name: 'Group' })).toHaveTextContent(
      'fast → vip'
    )
    await user.click(screen.getByRole('button', { name: 'Submit key' }))
    await waitFor(() => expect(submitted).toHaveBeenCalled())
    expect(submitted.mock.calls[0][0]).toMatchObject({
      group: 'auto',
      auto_groups_mode: 'custom',
      auto_groups: ['fast', 'vip'],
    })
  })

  it('selects and reorders multiple authorized groups without virtual Auto permission', async () => {
    const user = userEvent.setup()
    render(<RoutingForm allowAuto={false} defaults={{ group: 'default' }} />)
    await user.click(screen.getByRole('combobox', { name: 'Group' }))
    await user.click(screen.getByRole('option', { name: /^vip/ }))
    await user.keyboard('{Escape}')
    expect(screen.getByRole('combobox', { name: 'Group' })).toHaveTextContent(
      'default → vip'
    )
    expect(
      screen.queryByRole('button', { name: 'Restore global Auto' })
    ).not.toBeInTheDocument()
    const move = screen.getByRole('button', { name: 'Move vip up' })
    move.focus()
    await user.keyboard('{Enter}')
    expect(screen.getByRole('combobox', { name: 'Group' })).toHaveTextContent(
      'vip → default'
    )
    await user.click(screen.getByRole('switch', { name: 'Smart Routing' }))
    expect(screen.getByRole('button', { name: 'Move vip down' })).toBeDisabled()
    await user.click(screen.getByRole('switch', { name: 'Smart Routing' }))
    await user.click(screen.getByRole('button', { name: 'Submit key' }))
    await waitFor(() => expect(submitted).toHaveBeenCalled())
    expect(submitted.mock.calls[0][0]).toMatchObject({
      group: 'auto',
      auto_groups_mode: 'custom',
      auto_groups: ['vip', 'default'],
      smart_routing: false,
      cross_group_retry: true,
    })
  })

  it('keeps an emptied manual selection explicit instead of silently inheriting another group', async () => {
    const user = userEvent.setup()
    render(<RoutingForm allowAuto={false} defaults={{ group: 'default' }} />)
    await user.click(screen.getByRole('button', { name: 'Remove default' }))
    expect(screen.getByRole('combobox', { name: 'Group' })).toHaveTextContent(
      'Select groups in routing order'
    )
    await user.click(screen.getByRole('button', { name: 'Submit key' }))
    await waitFor(() => expect(submitted).toHaveBeenCalled())
    expect(submitted.mock.calls[0][0]).toMatchObject({
      group: 'auto',
      auto_groups_mode: 'custom',
      auto_groups: [],
    })
  })

  it('saves only group names in the current account preset and hides them after an account switch', async () => {
    const user = userEvent.setup()
    render(<RoutingForm />)
    await user.click(screen.getByRole('button', { name: 'Save as preset' }))
    await user.type(screen.getByLabelText('Preset name'), 'For work')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
    expect(
      JSON.parse(
        localStorage.getItem('newapi:key-group-presets:v1:7') ?? 'null'
      )
    ).toEqual([{ name: 'For work', groups: ['vip', 'default'] }])
    act(() =>
      useAuthStore
        .getState()
        .auth.setUser({ id: 8, username: 'other', role: 1 })
    )
    expect(
      screen.getByRole('combobox', { name: 'Group preset' })
    ).toBeDisabled()
    expect(localStorage.getItem('newapi:key-group-presets:v1:8')).toBeNull()
  })

  it('uses keyboard radio navigation without changing the retained group draft', async () => {
    const user = userEvent.setup()
    render(<RoutingForm defaults={{ smart_routing: true }} />)
    const first = screen.getByRole('radio', {
      name: 'Smart Auto (best overall)',
    })
    first.focus()
    await user.keyboard('{ArrowRight}')
    expect(
      screen.getByRole('radio', { name: 'Success rate first (most stable)' })
    ).toBeChecked()
    expect(screen.getByRole('combobox', { name: 'Group' })).toHaveTextContent(
      'vip → default'
    )
  })
})

it('does not display invented success percentages when group metrics are absent', () => {
  render(
    <I18nextProvider i18n={i18n}>
      <ApiKeyGroupHealth />
    </I18nextProvider>
  )
  expect(screen.getByText('No performance data')).toBeVisible()
  expect(screen.queryByText(/% success rate/)).not.toBeInTheDocument()
})

it('does not allow presets to be saved without an authenticated account', () => {
  useAuthStore.getState().auth.reset()
  render(<RoutingForm />)
  expect(screen.getByRole('button', { name: 'Save as preset' })).toBeDisabled()
  fireEvent.click(screen.getByRole('button', { name: 'Save as preset' }))
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})

it('never silently drops other inherited groups when the user removes one above the custom-selection limit', async () => {
  const user = userEvent.setup()
  render(<RoutingForm globalGroups={['fast', 'vip', 'default']} maxCount={1} />)
  await user.click(screen.getByRole('combobox', { name: 'Group' }))
  await user.click(screen.getByRole('option', { name: /^fast/ }))
  await user.keyboard('{Escape}')
  expect(screen.getByRole('combobox', { name: 'Group' })).toHaveTextContent(
    'vip → default'
  )
})
