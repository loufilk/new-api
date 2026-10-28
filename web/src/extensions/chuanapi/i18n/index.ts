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
import type { ExtensionLocaleBundle, ExtensionLocaleKey } from '../../i18n'

type LocaleModule = { default: ExtensionLocaleBundle } | ExtensionLocaleBundle

function normalizeBundle(mod: LocaleModule): ExtensionLocaleBundle {
  if (mod && typeof mod === 'object' && 'default' in mod) {
    return (mod as { default: ExtensionLocaleBundle }).default
  }
  return mod as ExtensionLocaleBundle
}

const chuanapiLocaleLoaders: Record<
  ExtensionLocaleKey,
  () => Promise<LocaleModule>
> = {
  en: () => import('./locales/en.json'),
  zhCN: () => import('./locales/zh.json'),
  fr: () => import('./locales/fr.json'),
  ru: () => import('./locales/ru.json'),
  ja: () => import('./locales/ja.json'),
  vi: () => import('./locales/vi.json'),
  zhTW: () => import('./locales/zh-TW.json'),
}

/**
 * Async single-locale loader so the chuanapi extension no longer pulls all
 * seven locales into the sync entry. Registration-order merge semantics are
 * preserved by the generic bridge in `@/extensions/i18n`.
 */
export async function loadChuanapiI18nOverride(
  locale: ExtensionLocaleKey
): Promise<ExtensionLocaleBundle | undefined> {
  const loader = chuanapiLocaleLoaders[locale]
  if (!loader) return undefined
  try {
    return normalizeBundle(await loader())
  } catch {
    return undefined
  }
}
