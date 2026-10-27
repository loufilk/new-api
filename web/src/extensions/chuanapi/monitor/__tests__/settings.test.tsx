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
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { beforeEach, describe, expect, onTestFinished, test, vi } from 'vitest'

import { SettingsPageProvider } from '@/features/system-settings/components/settings-page-context'
import { api } from '@/lib/api'

import en from '../../i18n/locales/en.json'
import { MonitorSettings } from '../settings'
import type { MonitorConfig, MonitorTarget } from '../types'

const groupID = `route-${'a'.repeat(32)}`
const saved: MonitorConfig = {
  enabled: true,
  title: '',
  description: '',
  groups: { [groupID]: 'Premium' },
  hidden_groups: {},
}

function renderSettings(
  config = saved,
  targetsFail = false,
  targets: MonitorTarget[] = [
    { id: groupID, number: 1, model: 'example-model' },
  ]
) {
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url.endsWith('/targets')) {
      if (targetsFail) throw new Error('offline')
      return {
        data: {
          success: true,
          data: targets,
        },
      }
    }
    return {
      data: { success: true, data: { config, environment_enabled: true } },
    }
  })
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  })
  const actions = document.createElement('div')
  document.body.append(actions)
  const view = render(
    <QueryClientProvider client={client}>
      <SettingsPageProvider actionsContainer={actions}>
        <MonitorSettings />
      </SettingsPageProvider>
    </QueryClientProvider>
  )
  onTestFinished(() => {
    view.unmount()
    client.clear()
    actions.remove()
  })
  return view
}

beforeEach(async () => {
  i18next.addResourceBundle('en', 'translation', en.translation, true, true)
  await i18next.changeLanguage('en')
})

describe('Model monitor settings', () => {
  test('saves page text, visibility and group names together and uses the normalized saved values', async () => {
    const user = userEvent.setup()
    vi.spyOn(api, 'put').mockImplementation(async (_url, value) => ({
      data: { success: true, data: value },
    }))
    renderSettings()
    const name = await screen.findByLabelText('Reference route 1 example-model')
    await user.clear(name)
    await user.type(name, 'VIP group')
    await user.type(screen.getByLabelText('Page title'), 'Our monitor')
    await user.click(
      screen.getByRole('switch', { name: 'Enable monitoring page' })
    )
    await user.click(screen.getByRole('button', { name: 'Save Changes' }))
    await waitFor(() =>
      expect(api.put).toHaveBeenCalledWith('/api/option/model-monitor', {
        enabled: false,
        title: 'Our monitor',
        description: '',
        groups: { [groupID]: 'VIP group' },
        hidden_groups: { [groupID]: false },
      })
    )
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Save Changes' })
      ).toBeDisabled()
    )
    expect(
      screen.getByRole('switch', { name: 'Enable monitoring page' })
    ).not.toBeChecked()
  })

  test('hides one group without clearing its name or disabling the page', async () => {
    const user = userEvent.setup()
    vi.spyOn(api, 'put').mockResolvedValue({
      data: {
        success: true,
        data: { ...saved, hidden_groups: { [groupID]: true } },
      },
    })
    renderSettings()
    const toggle = await screen.findByRole('switch', {
      name: 'Show Reference route 1',
    })
    expect(toggle).toBeChecked()
    toggle.focus()
    await user.keyboard(' ')
    expect(toggle).not.toBeChecked()
    await user.click(screen.getByRole('button', { name: 'Save Changes' }))
    await waitFor(() =>
      expect(api.put).toHaveBeenCalledWith('/api/option/model-monitor', {
        ...saved,
        hidden_groups: { [groupID]: true },
      })
    )
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Save Changes' })
      ).toBeDisabled()
    )
    expect(
      screen.getByLabelText('Reference route 1 example-model')
    ).toHaveValue('Premium')
    expect(
      screen.getByRole('switch', { name: 'Enable monitoring page' })
    ).toBeChecked()
    expect(toggle).not.toBeChecked()
  })

  test('can restore a hidden unnamed group when the current catalog is unavailable', async () => {
    const user = userEvent.setup()
    const hidden = { ...saved, groups: {}, hidden_groups: { [groupID]: true } }
    vi.spyOn(api, 'put').mockResolvedValue({
      data: { success: true, data: { ...hidden, hidden_groups: {} } },
    })
    renderSettings(hidden, true)
    const toggle = await screen.findByRole('switch', {
      name: 'Show Previously saved group',
    })
    expect(toggle).not.toBeChecked()
    await user.click(toggle)
    await user.click(screen.getByRole('button', { name: 'Save Changes' }))
    await waitFor(() =>
      expect(api.put).toHaveBeenCalledWith('/api/option/model-monitor', {
        ...hidden,
        groups: { [groupID]: '' },
        hidden_groups: { [groupID]: false },
      })
    )
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Save Changes' })
      ).toBeDisabled()
    )
  })

  test('keeps saved group names editable when the current upstream catalog is unavailable', async () => {
    renderSettings(saved, true)
    expect(
      await screen.findByText('Unable to load the current groups')
    ).toBeVisible()
    expect(screen.getByLabelText('Previously saved group')).toHaveValue(
      'Premium'
    )
    expect(screen.getByLabelText('Page title')).toBeEnabled()
  })

  test('showing a hidden group and clearing its name stays clean after normalized save and reset', async () => {
    const user = userEvent.setup()
    const hidden = { ...saved, hidden_groups: { [groupID]: true } }
    vi.spyOn(api, 'put').mockResolvedValue({
      data: { success: true, data: { ...saved, groups: {} } },
    })
    renderSettings(hidden)
    const toggle = await screen.findByRole('switch', {
      name: 'Show Reference route 1',
    })
    const name = screen.getByLabelText('Reference route 1 example-model')
    expect(toggle).not.toBeChecked()
    await user.click(toggle)
    await user.clear(name)
    await user.click(screen.getByRole('button', { name: 'Save Changes' }))
    await waitFor(() =>
      expect(api.put).toHaveBeenCalledWith('/api/option/model-monitor', {
        ...saved,
        groups: { [groupID]: '' },
        hidden_groups: { [groupID]: false },
      })
    )
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Save Changes' })
      ).toBeDisabled()
    )
    expect(toggle).toBeChecked()
    expect(name).toHaveValue('')

    await user.click(toggle)
    await user.type(name, 'Unsaved name')
    await user.click(screen.getByRole('button', { name: 'Reset' }))
    expect(toggle).toBeChecked()
    expect(name).toHaveValue('')
    expect(screen.getByRole('button', { name: 'Save Changes' })).toBeDisabled()
  })

  test.each(['business failure', 'network failure'])(
    '%s keeps the unsaved visibility choice until reset restores the saved hidden state',
    async (failure) => {
      const user = userEvent.setup()
      const put = vi.spyOn(api, 'put')
      if (failure === 'business failure') {
        put.mockResolvedValue({
          data: { success: false, message: 'monitor.settings.saveFailed' },
        })
      } else {
        put.mockRejectedValue(new Error('offline'))
      }
      renderSettings({ ...saved, hidden_groups: { [groupID]: true } })
      const toggle = await screen.findByRole('switch', {
        name: 'Show Reference route 1',
      })
      expect(toggle).not.toBeChecked()
      await user.click(toggle)
      await user.click(screen.getByRole('button', { name: 'Save Changes' }))
      await waitFor(() => expect(put).toHaveBeenCalled())
      await waitFor(() =>
        expect(
          screen.getByRole('button', { name: 'Save Changes' })
        ).toBeEnabled()
      )
      expect(toggle).toBeChecked()
      await user.click(screen.getByRole('button', { name: 'Reset' }))
      expect(toggle).not.toBeChecked()
      expect(
        screen.getByLabelText('Reference route 1 example-model')
      ).toHaveValue('Premium')
      expect(
        screen.getByRole('button', { name: 'Save Changes' })
      ).toBeDisabled()
    }
  )

  test('pending visibility save disables editing and repeated save clicks until the response arrives', async () => {
    const user = userEvent.setup()
    const response = {
      data: {
        success: true,
        data: { ...saved, hidden_groups: { [groupID]: true } },
      },
    }
    let finishSave: (value: typeof response) => void = () => {
      throw new Error('The save request has not started')
    }
    const pending = new Promise<typeof response>((resolve) => {
      finishSave = resolve
    })
    const put = vi.spyOn(api, 'put').mockImplementation(() => pending)
    renderSettings()
    const toggle = await screen.findByRole('switch', {
      name: 'Show Reference route 1',
    })
    await user.click(toggle)
    const save = screen.getByRole('button', { name: 'Save Changes' })
    await user.click(save)
    await waitFor(() => expect(toggle).toHaveAttribute('aria-disabled', 'true'))
    expect(
      screen.getByLabelText('Reference route 1 example-model')
    ).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Reset' })).toBeDisabled()
    expect(save).toBeDisabled()
    await user.click(toggle)
    await user.click(save)
    expect(toggle).not.toBeChecked()
    expect(put).toHaveBeenCalledTimes(1)
    finishSave(response)
    await waitFor(() =>
      expect(toggle).not.toHaveAttribute('aria-disabled', 'true')
    )
    expect(toggle).not.toBeChecked()
    expect(screen.getByRole('button', { name: 'Save Changes' })).toBeDisabled()
  })

  test('hiding one of multiple groups preserves another group hidden state and both names', async () => {
    const user = userEvent.setup()
    const secondID = `route-${'b'.repeat(32)}`
    const config = {
      ...saved,
      groups: { [groupID]: 'Premium', [secondID]: 'Private' },
      hidden_groups: { [secondID]: true },
    }
    vi.spyOn(api, 'put').mockImplementation(async (_url, value) => ({
      data: { success: true, data: value },
    }))
    renderSettings(config, false, [
      { id: groupID, number: 1, model: 'example-model' },
      { id: secondID, number: 2, model: 'other-model' },
    ])
    await user.click(
      await screen.findByRole('switch', { name: 'Show Reference route 1' })
    )
    expect(
      screen.getByRole('switch', { name: 'Show Reference route 2' })
    ).not.toBeChecked()
    await user.click(screen.getByRole('button', { name: 'Save Changes' }))
    await waitFor(() =>
      expect(api.put).toHaveBeenCalledWith('/api/option/model-monitor', {
        ...config,
        hidden_groups: { [groupID]: true, [secondID]: true },
      })
    )
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Save Changes' })
      ).toBeDisabled()
    )
  })

  test('does not overwrite a dirty form after a failed save and can reset to saved values', async () => {
    const user = userEvent.setup()
    vi.spyOn(api, 'put').mockResolvedValue({
      data: { success: false, message: 'monitor.settings.saveFailed' },
    })
    renderSettings()
    const input = await screen.findByLabelText('Page title')
    await user.type(input, 'Unsaved title')
    await user.click(screen.getByRole('button', { name: 'Save Changes' }))
    await waitFor(() => expect(api.put).toHaveBeenCalled())
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Save Changes' })).toBeEnabled()
    )
    expect(input).toHaveValue('Unsaved title')
    await user.click(screen.getByRole('button', { name: 'Reset' }))
    expect(input).toHaveValue('')
  })

  test('rejects overlong text without making a save request', async () => {
    vi.spyOn(api, 'put')
    renderSettings()
    const input = await screen.findByLabelText('Page title')
    fireEvent.change(input, { target: { value: 'x'.repeat(121) } })
    fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    await waitFor(() => expect(input).toHaveAttribute('aria-invalid', 'true'))
    expect(api.put).not.toHaveBeenCalled()
  })
})
