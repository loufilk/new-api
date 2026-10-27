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
import { describe, expect, test } from 'vitest'

import {
  extensionBrandFavicon,
  extensionBrandLogo,
  extensionBrandLogoClassName,
  setBrandExtension,
} from '../brand'
import {
  extensionFlags,
  isExtensionFeatureEnabled,
  setExtensionFlags,
} from '../flags'
import {
  extensionI18nOverrides,
  loadExtensionI18nOverride,
  setI18nExtensions,
} from '../i18n'

describe('frontend brand extension bridge', () => {
  test('falls back to the default logo until an extension registers one, then rejects reconfiguration', () => {
    expect(extensionBrandLogo()).toBe('/logo.png')
    expect(extensionBrandFavicon('/custom-icon.png')).toBe('/custom-icon.png')
    expect(extensionBrandLogoClassName()).toBeUndefined()

    setBrandExtension({
      id: 'test/brand',
      logoUrl: '/brand/test-mark.png',
      faviconUrl: '/brand/test-icon.png',
      logoClassName: 'dark:invert',
    })
    expect(extensionBrandLogo()).toBe('/brand/test-mark.png')
    expect(extensionBrandFavicon('/custom-icon.png')).toBe(
      '/brand/test-icon.png'
    )
    expect(extensionBrandLogoClassName()).toBe('dark:invert')

    expect(() =>
      setBrandExtension({ id: 'other/brand', logoUrl: '/brand/other.png' })
    ).toThrow('frontend brand extension is already configured')
  })
})

describe('frontend extension feature flag bridge', () => {
  test('starts disabled, reflects configured values, and rejects reconfiguration', () => {
    expect(isExtensionFeatureEnabled('showLegacyAffiliateQuota')).toBe(false)
    expect(extensionFlags.showLegacyAffiliateQuota).toBe(false)

    setExtensionFlags({ showLegacyAffiliateQuota: true })
    expect(isExtensionFeatureEnabled('showLegacyAffiliateQuota')).toBe(true)
    expect(extensionFlags.showLegacyAffiliateQuota).toBe(true)

    expect(() =>
      setExtensionFlags({ showLegacyAffiliateQuota: false })
    ).toThrow(
      'frontend extension flag is already configured: showLegacyAffiliateQuota'
    )
  })
})

describe('frontend i18n override bridge', () => {
  test('merges static and async overrides in registration order, then rejects reconfiguration', async () => {
    expect(extensionI18nOverrides()).toEqual({})

    setI18nExtensions([
      {
        id: 'first/i18n',
        overrides: {
          en: { translation: { Greeting: 'Hello', Farewell: 'Bye' } },
          zhCN: { translation: { Greeting: '你好' } },
        },
      },
      {
        id: 'second/i18n',
        overrides: {
          en: { translation: { Greeting: 'Hey' } },
        },
        loadOverride: async (locale) =>
          locale === 'en'
            ? { translation: { Greeting: 'Howdy', Async: 'loaded' } }
            : undefined,
      },
    ])

    const overrides = extensionI18nOverrides()
    expect(overrides.en?.translation).toEqual({
      Greeting: 'Hey',
      Farewell: 'Bye',
    })
    expect(overrides.zhCN?.translation).toEqual({ Greeting: '你好' })
    await expect(loadExtensionI18nOverride('en')).resolves.toEqual({
      translation: {
        Greeting: 'Howdy',
        Farewell: 'Bye',
        Async: 'loaded',
      },
    })

    expect(() => setI18nExtensions([])).toThrow(
      'frontend i18n extensions are already configured'
    )
  })
})
