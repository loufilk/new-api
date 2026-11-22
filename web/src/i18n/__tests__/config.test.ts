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
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

beforeEach(async () => {
  vi.resetModules()
  vi.stubEnv('DEV', false)
  localStorage.clear()
  window.localStorage.clear()
  const i18next = await vi.importActual<typeof import('i18next')>('i18next')
  // The shared setup initializes the package singleton. Use a fresh real
  // instance so each case exercises the app's first initialization path.
  vi.doMock('i18next', () => ({
    ...i18next,
    default: i18next.createInstance(),
  }))
})

afterEach(() => {
  vi.unstubAllEnvs()
  vi.doUnmock('i18next')
  localStorage.clear()
  window.localStorage.clear()
})

describe('lazy interface translations', () => {
  test('restores a saved Traditional Chinese alias without switching to Simplified Chinese', async () => {
    window.localStorage.setItem('i18nextLng', ' zhtw ')
    const { initI18n } = await import('../config')

    const instance = await initI18n()

    expect(instance.resolvedLanguage).toBe('zhTW')
    expect(instance.t('fingerprint.title')).toBe('模型智力監測')
    expect(window.localStorage.getItem('i18nextLng')).toBe('zhTW')
  })

  test.each([
    ['en', 'Full capability', 'Reduced capability'],
    ['zhCN', '满血', '降智'],
    ['zhTW', '滿血', '降智'],
    ['fr', 'Pleine capacité', 'Capacité réduite'],
    ['ja', '通常性能', '性能低下'],
    ['ru', 'Качество в норме', 'Качество снижено'],
    ['vi', 'Năng lực bình thường', 'Năng lực suy giảm'],
  ])(
    'loads monitoring status labels when switching to %s',
    async (language, full, reduced) => {
      window.localStorage.setItem('i18nextLng', 'en')
      const { setI18nExtensions } = await import('@/extensions/i18n')
      const { loadChuanapiI18nOverride } =
        await import('@/extensions/chuanapi/i18n')
      setI18nExtensions([
        { id: 'chuanapi', loadOverride: loadChuanapiI18nOverride },
      ])
      const { initI18n } = await import('../config')
      const instance = await initI18n()

      await instance.changeLanguage(language)

      expect(instance.resolvedLanguage).toBe(language)
      expect(instance.t('Full capability')).toBe(full)
      expect(instance.t('Reduced capability')).toBe(reduced)
      expect(window.localStorage.getItem('i18nextLng')).toBe(language)
    }
  )

  test('keeps the most recently selected language when an earlier bundle finishes later', async () => {
    window.localStorage.setItem('i18nextLng', 'en')
    let markFrenchStarted!: () => void
    const frenchStarted = new Promise<void>((resolve) => {
      markFrenchStarted = resolve
    })
    let releaseFrench!: () => void
    const frenchResponse = new Promise<void>((resolve) => {
      releaseFrench = resolve
    })
    const { setI18nExtensions } = await import('@/extensions/i18n')
    const { loadChuanapiI18nOverride } =
      await import('@/extensions/chuanapi/i18n')
    setI18nExtensions([
      {
        id: 'chuanapi',
        loadOverride: async (language) => {
          if (language === 'fr') {
            markFrenchStarted()
            await frenchResponse
          }
          return loadChuanapiI18nOverride(language)
        },
      },
    ])
    const { initI18n } = await import('../config')
    const instance = await initI18n()
    const earlierChange = instance.changeLanguage('fr')
    await frenchStarted

    try {
      await instance.changeLanguage('ja')
      expect(instance.t('Full capability')).toBe('通常性能')
    } finally {
      releaseFrench()
    }
    await earlierChange

    expect(instance.resolvedLanguage).toBe('ja')
    expect(instance.t('fingerprint.title')).toBe('モデル性能モニタリング')
    expect(window.localStorage.getItem('i18nextLng')).toBe('ja')
  })

  test('uses the English bundle for a malformed saved language', async () => {
    window.localStorage.setItem('i18nextLng', 'invalid_locale!')
    const { initI18n } = await import('../config')

    const instance = await initI18n()

    expect(instance.resolvedLanguage).toBe('en')
    expect(instance.t('fingerprint.title')).toBe(
      'Model Intelligence Monitoring'
    )
  })
})
