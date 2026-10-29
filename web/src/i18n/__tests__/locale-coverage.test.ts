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
import { createInstance } from 'i18next'
import { describe, expect, test } from 'vitest'

import en from '../locales/en.json'
import fr from '../locales/fr.json'
import ja from '../locales/ja.json'
import ru from '../locales/ru.json'
import vi from '../locales/vi.json'
import zhTW from '../locales/zh-TW.json'
import zhCN from '../locales/zh.json'

const resources = { en, fr, ja, ru, vi, zhCN, zhTW }

describe('locale coverage after upstream integration', () => {
  test.each(Object.keys(resources))(
    '%s resolves retained upstream interface and redemption messages without fallback',
    async (language) => {
      const i18n = createInstance()
      await i18n.init({ lng: language, fallbackLng: false, resources })

      for (const key of [
        'Changelog',
        'After conversion',
        'Image Cache',
        'System updates',
        'Use a Passkey from another website',
        'Used redemption codes cannot be modified',
        'Agent ID',
        'Provider agent ID',
        'Available quota',
        'Invitation policy',
      ]) {
        expect(i18n.exists(key), `${language}: ${key}`).toBe(true)
        expect(i18n.t(key)).not.toBe('')
      }
    }
  )

  test('switching from English to Chinese renders restored interface translations', async () => {
    const i18n = createInstance()
    await i18n.init({ lng: 'en', fallbackLng: false, resources })
    expect(i18n.t('System updates')).toBe('System updates')

    await i18n.changeLanguage('zhCN')
    expect(i18n.t('System updates')).toBe('系统更新')
    expect(i18n.t('Image Cache')).toBe('图片缓存')
    expect(i18n.t('After conversion')).toBe('迁移后')
    expect(i18n.t('Use a Passkey from another website')).toBe(
      '使用其他网站的通行密钥'
    )
    expect(i18n.t('Used redemption codes cannot be modified')).toBe(
      '已使用的兑换码不能修改'
    )
  })

  test('switching languages keeps reseller accounts distinct from provider agents', async () => {
    const i18n = createInstance()
    await i18n.init({ lng: 'en', fallbackLng: false, resources })
    expect(i18n.t('Agent ID')).toBe('Agent ID')
    expect(i18n.t('Provider agent ID')).toBe('Provider agent ID')

    await i18n.changeLanguage('zhCN')
    expect(i18n.t('Agent ID')).toBe('代理用户 ID')
    expect(i18n.t('Provider agent ID')).toBe('服务商智能体 ID')

    await i18n.changeLanguage('zhTW')
    expect(i18n.t('Agent ID')).toBe('代理使用者 ID')
    expect(i18n.t('Provider agent ID')).toBe('供應商智慧代理 ID')
  })
})
