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
import i18n, { type BackendModule } from 'i18next'
import LanguageDetector from 'i18next-browser-languagedetector'
import { initReactI18next } from 'react-i18next'

import {
  loadExtensionI18nOverride,
  type ExtensionLocaleKey,
} from '@/extensions'

type Bundle = { translation: Record<string, string> }
type LocaleModule = { default: Bundle } | Bundle

const SUPPORTED_LNGS = ['en', 'zhCN', 'fr', 'ru', 'ja', 'vi', 'zhTW'] as const

type SupportedLng = (typeof SUPPORTED_LNGS)[number]

const coreLocaleLoaders: Record<SupportedLng, () => Promise<LocaleModule>> = {
  en: () => import('./locales/en.json'),
  zhCN: () => import('./locales/zh.json'),
  fr: () => import('./locales/fr.json'),
  ru: () => import('./locales/ru.json'),
  ja: () => import('./locales/ja.json'),
  vi: () => import('./locales/vi.json'),
  zhTW: () => import('./locales/zh-TW.json'),
}

function resolveSupportedLanguage(language: string): SupportedLng {
  const normalized = language.trim().replaceAll('_', '-').toLowerCase()
  if (normalized.startsWith('zh')) {
    if (
      normalized === 'zh-tw' ||
      normalized === 'zh-hk' ||
      normalized === 'zh-mo' ||
      normalized.startsWith('zh-hant') ||
      language === 'zhTW'
    ) {
      return 'zhTW'
    }
    return 'zhCN'
  }

  const base = normalized.split('-')[0]
  if (base === 'fr' || base === 'ru' || base === 'ja' || base === 'vi') {
    return base
  }
  return 'en'
}

function normalizeBundle(module: LocaleModule): Bundle {
  if (module && typeof module === 'object' && 'default' in module) {
    return module.default
  }
  return module
}

async function loadLocalizedBundle(locale: SupportedLng): Promise<Bundle> {
  const [coreModule, extension] = await Promise.all([
    coreLocaleLoaders[locale](),
    loadExtensionI18nOverride(locale as ExtensionLocaleKey),
  ])
  const core = normalizeBundle(coreModule)
  if (!extension) return core
  return {
    translation: {
      ...core.translation,
      ...extension.translation,
    },
  }
}

const localeBackend: BackendModule = {
  type: 'backend',
  init() {},
  read(language, _namespace, callback) {
    void (async () => {
      try {
        const bundle = await loadLocalizedBundle(
          resolveSupportedLanguage(language)
        )
        callback(null, bundle.translation)
      } catch (error) {
        callback(
          error instanceof Error ? error : new Error('Failed to load locale'),
          false
        )
      }
    })()
  },
}

let initPromise: Promise<typeof i18n> | null = null

/** Initialize i18next before React mounts while loading only active locales. */
export function initI18n(): Promise<typeof i18n> {
  if (initPromise) return initPromise
  initPromise = i18n
    .use(localeBackend)
    .use(LanguageDetector)
    .use(initReactI18next)
    .init({
      fallbackLng: 'en',
      supportedLngs: [...SUPPORTED_LNGS],
      load: 'currentOnly',
      nsSeparator: false,
      debug: import.meta.env.DEV,
      interpolation: {
        escapeValue: false,
      },
      detection: {
        order: ['localStorage', 'navigator'],
        caches: ['localStorage'],
        convertDetectedLanguage: resolveSupportedLanguage,
      },
    })
    .then(() => i18n)
  return initPromise
}

export default i18n
