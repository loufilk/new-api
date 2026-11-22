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
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, expect, it, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import fr from '@/i18n/locales/fr.json'
import ja from '@/i18n/locales/ja.json'
import ru from '@/i18n/locales/ru.json'
import viLocale from '@/i18n/locales/vi.json'
import zhTW from '@/i18n/locales/zh-TW.json'
import zhCN from '@/i18n/locales/zh.json'
import { useAuthStore } from '@/stores/auth-store'

import { UserCallInterceptionSection } from '../section'
import { config, setupRequests } from './fixtures'

const resources = { en, fr, ja, ru, vi: viLocale, zhCN, zhTW }
const clients: QueryClient[] = []

async function renderLanguage(language: string) {
  const instance = createInstance()
  await instance.init({
    lng: language,
    fallbackLng: 'en',
    resources,
    interpolation: { escapeValue: false },
  })
  useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role: 100 })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  render(
    <I18nextProvider i18n={instance}>
      <QueryClientProvider client={client}>
        <UserCallInterceptionSection userId={12} targetRole={1} />
      </QueryClientProvider>
    </I18nextProvider>
  )
  return instance
}

afterEach(() => {
  cleanup()
  for (const client of clients) client.clear()
  clients.length = 0
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})

it.each([
  ['en', '123,456'],
  ['zhCN', '123,456'],
  ['zhTW', '123,456'],
  ['fr', '123\u202f456'],
  ['ru', '123\u00a0456'],
  ['ja', '123,456'],
  ['vi', '123.456'],
  ['invalid-language', '123,456'],
])(
  'renders translated interception labels and valid number formatting for %s',
  async (language, expectedNumber) => {
    const { get } = setupRequests()
    get.mockImplementation(async (url) => {
      if (url.endsWith('/hits')) {
        return {
          data: {
            success: true,
            data: {
              items: [
                {
                  id: 1,
                  time: 1793458800,
                  user_id: 12,
                  token_id: 123456,
                  actor_user_id: 12,
                  identity_kind: 'api_key',
                  request_id: 'request-localized',
                  interface: '/v1/chat/completions',
                  protocol: 'openai_chat',
                  status: 200,
                  revision: 1,
                },
              ],
              total: 1,
              page: 1,
              page_size: 10,
            },
          },
        }
      }
      return { data: { success: true, data: config } }
    })
    const instance = await renderLanguage(language)
    await screen.findByRole('textbox', { name: instance.t('Custom reply') })
    expect(
      screen.getByText(
        instance.t('User-level call interception and custom replies')
      )
    ).toBeInTheDocument()
    await userEvent.click(
      screen.getByRole('tab', { name: instance.t('Interception hits') })
    )
    expect(
      await screen.findByText(expectedNumber, { normalizer: (text) => text })
    ).toBeInTheDocument()
  }
)

it('updates labels when language changes without losing an unsaved bilingual reply', async () => {
  setupRequests()
  const instance = await renderLanguage('en')
  const reply = await screen.findByRole('textbox', { name: 'Custom reply' })
  fireEvent.change(reply, { target: { value: '未保存\nUnsaved' } })
  await act(async () => {
    await instance.changeLanguage('zhCN')
  })
  expect(screen.getByText('用户级调用拦截与自定义回复')).toBeInTheDocument()
  expect(screen.getByRole('textbox', { name: '自定义回复' })).toHaveValue(
    '未保存\nUnsaved'
  )
  expect(screen.getByRole('button', { name: '保存拦截配置' })).toBeEnabled()
})
