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
import { act, render, screen } from '@testing-library/react'
import { createInstance } from 'i18next'
import { I18nextProvider, initReactI18next } from 'react-i18next'
import { expect, test } from 'vitest'

import en from '@/i18n/locales/en.json'
import fr from '@/i18n/locales/fr.json'
import ja from '@/i18n/locales/ja.json'
import ru from '@/i18n/locales/ru.json'
import vi from '@/i18n/locales/vi.json'
import zhTW from '@/i18n/locales/zh-TW.json'
import zh from '@/i18n/locales/zh.json'

import { ChannelConfiguration } from '../drawers/channel-configuration'

const resources = { en, fr, ja, ru, vi, zh, 'zh-TW': zhTW }
const configurationKeys = [
  'Channel model owner label',
  'Stored as channel metadata. Public model lists and details use the site-wide owner setting. This label does not select a routing channel.',
  'Use channel name as label',
  'Channel model owner label must be at most 64 characters without control or formatting characters',

  'Configure the connection and models, then create the channel.',
  'Channel configuration',
  'Connection & Models',
  'Routing & Mapping',
  'Request & Response',
  'Other Settings',
  'Configure Models',
]

test.each(Object.entries(resources))(
  'the %s locale provides the channel configuration copy without English fallbacks',
  (language, resource) => {
    const translations: Record<string, string> = resource.translation
    for (const key of configurationKeys) {
      expect(translations[key], key).toEqual(expect.any(String))
      expect(translations[key].trim(), key).not.toBe('')
      if (language !== 'en') expect(translations[key], key).not.toBe(key)
    }
  }
)

test('changing the language updates configuration tab labels and their accessible name', async () => {
  const i18n = createInstance()
  await i18n
    .use(initReactI18next)
    .init({ lng: 'en', fallbackLng: 'en', resources })
  render(
    <I18nextProvider i18n={i18n}>
      <ChannelConfiguration
        section='connection'
        onSectionChange={() => undefined}
        statuses={{
          connection: 'ready',
          routing: 'idle',
          request: 'idle',
          other: 'idle',
        }}
        connection={null}
        models={null}
        quickOptions={null}
        routing={null}
        request={null}
        other={null}
      />
    </I18nextProvider>
  )
  expect(
    screen.getByRole('tablist', { name: 'Channel configuration' })
  ).toBeVisible()
  await act(() => i18n.changeLanguage('zh'))
  expect(screen.getByRole('tablist', { name: '渠道配置' })).toBeVisible()
  expect(screen.getByRole('tab', { name: /^连接与模型/ })).toHaveAttribute(
    'aria-selected',
    'true'
  )
  expect(screen.getByRole('tab', { name: '路由与映射' })).toBeVisible()
  expect(screen.getByRole('tab', { name: '请求与响应' })).toBeVisible()
  expect(screen.getByRole('tab', { name: '其他设置' })).toBeVisible()
})
