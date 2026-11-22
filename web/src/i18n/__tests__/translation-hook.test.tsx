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
import { act, renderHook } from '@testing-library/react'
import { createInstance } from 'i18next'
import type { PropsWithChildren } from 'react'
import { I18nextProvider, useTranslation } from 'react-i18next'
import { expect, test } from 'vitest'

import { formatGroupDetail } from '@/components/model-group-selector/group-detail'

test('a mounted translation hook updates the group detail when the language changes', async () => {
  const instance = createInstance()
  await instance.init({
    lng: 'en',
    resources: {
      en: { translation: {} },
      zhCN: {
        translation: {
          'User group': '用户分组',
          Auto: '自动',
          'Ratio: {{value}}': '倍率：{{value}}',
        },
      },
    },
  })
  const group = { desc: 'User group', ratio: 'auto' }
  const { result } = renderHook(
    () => formatGroupDetail(group, useTranslation().t),
    {
      wrapper: ({ children }: PropsWithChildren) => (
        <I18nextProvider i18n={instance}>{children}</I18nextProvider>
      ),
    }
  )

  expect(result.current).toBe('User group · Ratio: Auto')
  await act(async () => {
    await instance.changeLanguage('zhCN')
  })
  expect(result.current).toBe('用户分组 · 倍率：自动')
})
