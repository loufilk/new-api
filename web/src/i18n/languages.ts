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
export const INTERFACE_LANGUAGE_OPTIONS = [
  { code: 'zhCN', label: '简体中文' },
  { code: 'en', label: 'English' },
  { code: 'fr', label: 'Français' },
  { code: 'ru', label: 'Русский' },
  { code: 'ja', label: '日本語' },
  { code: 'vi', label: 'Tiếng Việt' },
  { code: 'zhTW', label: '繁體中文' },
] as const

export type InterfaceLanguageCode =
  (typeof INTERFACE_LANGUAGE_OPTIONS)[number]['code']

/** Resolve browser, saved-preference and interface codes consistently. */
export function normalizeInterfaceLanguage(
  value?: string | null
): InterfaceLanguageCode {
  const tag = toIntlLocale(value)
  if (!tag) return 'en'

  const locale = new Intl.Locale(tag)
  switch (locale.language) {
    case 'zh':
      // An explicit script takes precedence over the user's region.
      if (locale.script === 'Hant') return 'zhTW'
      if (locale.script === 'Hans') return 'zhCN'
      return ['TW', 'HK', 'MO'].includes(locale.region ?? '') ? 'zhTW' : 'zhCN'
    case 'en':
    case 'fr':
    case 'ru':
    case 'ja':
    case 'vi':
      return locale.language
    default:
      return 'en'
  }
}

/**
 * Map a browser-detected locale onto the interface language codes this project
 * uses with i18next (`zhCN` / `zhTW`).
 *
 * Browsers report standard BCP-47 tags (`zh-CN`, `zh-TW`, `zh-Hant`, `zh`, ...),
 * but `supportedLngs`/resources use the non-standard camelCase codes, so without
 * this mapping a Chinese browser would never match and fall back to English.
 * Non-Chinese codes are returned unchanged so i18next's own `supportedLngs`
 * matching still applies (e.g. `fr-FR` -> `fr`, `ja` -> `ja`).
 */
export function convertDetectedLanguage(value: string): string {
  const language = normalizeInterfaceLanguage(value)
  return language === 'zhCN' || language === 'zhTW' ? language : value
}

/**
 * Convert an interface language code (the values i18next uses, such as `zhCN` /
 * `zhTW`) into a valid BCP-47 locale tag that the `Intl.*` APIs accept.
 *
 * `new Intl.NumberFormat('zhCN')` throws `RangeError: Invalid language tag`, so
 * any locale derived from `i18n.language` / `i18n.resolvedLanguage` MUST be run
 * through this before it reaches an `Intl` constructor. Unknown values fall back
 * to `undefined`, which makes `Intl` use the runtime default locale.
 */
export function toIntlLocale(value?: string | null): string | undefined {
  if (!value) return undefined
  const normalized = value.trim().replaceAll('_', '-')
  switch (normalized.toLowerCase()) {
    case 'zhcn':
      return 'zh-CN'
    case 'zhtw':
      return 'zh-TW'
    default:
      break
  }
  try {
    return Intl.getCanonicalLocales(normalized)[0]
  } catch {
    return undefined
  }
}

/**
 * Convert the interface language to the locale tag understood by the backend
 * i18n middleware. Keeping this mapping next to the other locale conversions
 * ensures every API request follows the language currently shown in the UI.
 */
export function toBackendLanguage(value?: string | null): string {
  const language = normalizeInterfaceLanguage(value)
  switch (language) {
    case 'zhCN':
      return 'zh-CN'
    case 'zhTW':
      return 'zh-TW'
    default:
      return language
  }
}
