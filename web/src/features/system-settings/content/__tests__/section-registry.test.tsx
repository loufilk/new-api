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
import i18next from 'i18next'
import { useState } from 'react'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { SettingsPageProvider } from '../../components/settings-page-context'
import type { ContentSettings } from '../../types'
import {
  CONTENT_DEFAULT_SECTION,
  CONTENT_SECTION_IDS,
  getContentSectionContent,
  getContentSectionNavItems,
  type ContentSectionId,
} from '../section-registry'

const settings: ContentSettings = {
  'console_setting.api_info': '[]',
  'console_setting.announcements': '[]',
  'console_setting.faq': '[]',
  'console_setting.uptime_kuma_groups': '[]',
  'console_setting.api_info_enabled': true,
  'console_setting.announcements_enabled': true,
  'console_setting.faq_enabled': true,
  'console_setting.uptime_kuma_enabled': false,
  DataExportEnabled: false,
  DataExportDefaultTime: 'hour',
  DataExportInterval: 5,
  Chats: '[]',
  DrawingEnabled: false,
  MjNotifyEnabled: false,
  MjAccountFilterEnabled: false,
  MjForwardUrlEnabled: false,
  MjModeClearEnabled: false,
  MjActionCheckSuccessEnabled: false,
}

function SelectedSection(props: { section: ContentSectionId }) {
  const [actions, setActions] = useState<HTMLDivElement | null>(null)
  return (
    <SettingsPageProvider actionsContainer={actions}>
      <div ref={setActions} />
      {getContentSectionContent(props.section, settings)}
    </SettingsPageProvider>
  )
}

afterEach(() => useAuthStore.getState().auth.reset())

test('combined settings retain separate model, support, pool and announcement deep links', () => {
  const nav = getContentSectionNavItems(i18next.t)
  for (const section of [
    'model-tests',
    'support-email',
    'account-pool',
    'announcements',
  ]) {
    expect(
      nav.filter((item) => item.url === `/system-settings/content/${section}`)
    ).toHaveLength(1)
    expect(CONTENT_SECTION_IDS).toContain(section)
  }
  expect(CONTENT_DEFAULT_SECTION).toBe('dashboard')
})

test('switching between registered support and model settings saves only the selected capability', async () => {
  useAuthStore.getState().auth.setUser({ id: 7, username: 'owner', role: 100 })
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: Infinity },
      mutations: { retry: false },
    },
  })
  const status = {
    model_test_enabled: false,
    model_test_settings_available: true,
    account_pool_enabled: true,
    google_oauth: true,
  }
  const support = {
    enabled: false,
    admin_recipients: ['support@example.com'],
    site_origin: 'https://example.com',
    smtp_configured: true,
  }
  client.setQueryData(['system-options'], {
    data: [{ key: 'GroupRatio', value: '{"renamed":1}' }],
  })
  const optionsBefore = client.getQueryData(['system-options'])
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    let data: unknown = { items: [], total: 0 }
    if (url === '/api/support-email/settings') data = { ...support }
    if (url === '/api/status') data = { ...status }
    return { data: { success: true, data } }
  })
  const put = vi.spyOn(api, 'put').mockImplementation(async (url, input) => {
    if (typeof input !== 'object' || input === null) {
      throw new Error('Expected a settings object')
    }
    if (
      url === '/api/support-email/settings' &&
      'enabled' in input &&
      typeof input.enabled === 'boolean'
    ) {
      support.enabled = input.enabled
    }
    if (
      url === '/api/option/' &&
      'value' in input &&
      typeof input.value === 'boolean'
    ) {
      status.model_test_enabled = input.value
    }
    return { data: { success: true } }
  })
  const user = userEvent.setup()
  const ui = (section: ContentSectionId) => (
    <QueryClientProvider client={client}>
      <SelectedSection section={section} />
    </QueryClientProvider>
  )
  const view = render(ui('support-email'))
  await user.click(
    await screen.findByRole('switch', {
      name: 'Enable support email notifications',
    })
  )
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save Changes' })).toBeDisabled()
  )
  expect(status.model_test_enabled).toBe(false)
  view.rerender(ui('model-tests'))
  const modelSwitch = await screen.findByRole('switch', {
    name: 'Enable model performance tests',
  })
  expect(modelSwitch).not.toBeChecked()
  expect(
    screen.queryByRole('switch', { name: 'Enable support email notifications' })
  ).not.toBeInTheDocument()
  await user.click(modelSwitch)
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() =>
    expect(put).toHaveBeenLastCalledWith('/api/option/', {
      key: 'ModelTestEnabled',
      value: true,
    })
  )
  expect(support.enabled).toBe(true)
  expect(client.getQueryData(['status'])).toMatchObject({
    model_test_enabled: true,
    account_pool_enabled: true,
    google_oauth: true,
  })
  expect(client.getQueryData(['system-options'])).toEqual(optionsBefore)
  expect(put).toHaveBeenCalledTimes(2)
  view.unmount()
  client.clear()
})
