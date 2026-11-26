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
import { act, cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, expect, it, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import zhTW from '@/i18n/locales/zh-TW.json'
import zhCN from '@/i18n/locales/zh.json'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { GroupCatalogSettings } from '../group-catalog-settings'

const clients: QueryClient[] = []

async function openEditor(panel: 'groups' | 'personal') {
  const instance = createInstance()
  await instance.init({
    lng: 'en',
    fallbackLng: 'en',
    nsSeparator: false,
    resources: { en, zhCN, zhTW },
    interpolation: { escapeValue: false },
  })
  useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role: 100 })
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    let data: unknown
    if (url.endsWith('/group-catalog')) {
      data = {
        revision: 1,
        groups: {
          gold: {
            label: 'Custom Tier',
            sort_order: 0,
            badges: ['authentic'],
            exclusive: true,
          },
        },
      }
    } else if (url === '/api/pricing') {
      return {
        data: { success: true, data: [], usable_group: {}, group_ratio: {} },
      }
    } else if (url.startsWith('/api/user/search?')) {
      data = { items: [{ id: 2, username: 'sample' }] }
    } else if (url.endsWith('/group-entitlements/2')) {
      data = { revision: 1, groups: ['gold'] }
    } else if (url.endsWith('/user-group-pricing')) {
      data = {
        revision: 1,
        enabled: true,
        rules: [],
        available_groups: [
          { group: 'gold', inherited_ratio: 1, effective_ratio: 0 },
        ],
      }
    } else {
      throw new Error(`Unexpected GET: ${url}`)
    }
    return { data: { success: true, data } }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  render(
    <I18nextProvider i18n={instance}>
      <QueryClientProvider client={client}>
        <GroupCatalogSettings groupRatios='{"gold":1}' panel={panel} />
      </QueryClientProvider>
    </I18nextProvider>
  )
  return { instance, user: userEvent.setup() }
}

afterEach(() => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
  useAuthStore.getState().auth.reset()
})

it('updates editor labels and badge controls on language switch without changing group metadata or the draft', async () => {
  const { instance, user } = await openEditor('groups')
  await user.click(
    await screen.findByRole('button', {
      name: 'Open group details: Custom Tier (gold)',
    })
  )
  const label = screen.getByRole('textbox', { name: 'Display name' })
  await user.clear(label)
  await user.type(label, '自己命名')
  expect(
    screen.getByRole('button', { name: 'Move Authentic up' })
  ).toBeDisabled()
  await act(() => instance.changeLanguage('zhTW'))
  expect(screen.getByRole('button', { name: '上移 保真' })).toBeDisabled()
  expect(screen.getByRole('textbox', { name: '顯示名稱' })).toHaveValue(
    '自己命名'
  )
  await act(() => instance.changeLanguage('zhCN'))
  expect(screen.getByRole('button', { name: '保存分组展示' })).toBeEnabled()
  expect(screen.getByRole('textbox', { name: '显示名称' })).toHaveValue(
    '自己命名'
  )
})

it('preserves personal pricing drafts and translates the free rate and rollout lock instead of prompting a revision refresh', async () => {
  const { instance, user } = await openEditor('personal')
  await user.type(
    await screen.findByRole('textbox', { name: 'Username or user ID' }),
    'sample'
  )
  await user.click(screen.getByRole('button', { name: 'Search users' }))
  await user.selectOptions(
    await screen.findByRole('combobox', { name: /Target user/ }),
    '2'
  )
  await user.click(
    await screen.findByRole('button', { name: /Custom Tier.*0× · Free/ })
  )
  const field = screen.getByPlaceholderText('e.g. 0.5')
  await user.type(field, '0.25')
  const put = vi.spyOn(api, 'put').mockRejectedValue({
    isAxiosError: true,
    response: {
      status: 409,
      data: {
        code: 'IM21_ROLLOUT_LOCKED',
        message_key:
          'This change is locked until the im.21 rollout is complete.',
      },
    },
  })
  await user.click(screen.getByRole('button', { name: 'Save personal rate' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'This change is locked until the im.21 rollout is complete.'
  )
  expect(
    screen.queryByRole('button', { name: 'Continue editing after review' })
  ).not.toBeInTheDocument()
  await act(() => instance.changeLanguage('zhCN'))
  expect(screen.getByRole('alert')).toHaveTextContent(
    'im.21 滚动升级完成前，暂不允许此项修改。'
  )
  expect(
    screen.getByRole('button', { name: /Custom Tier.*0× · 免费/ })
  ).toBeInTheDocument()
  expect(field).toHaveValue('0.25')
  expect(put).toHaveBeenCalledTimes(1)
})
