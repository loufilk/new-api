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
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import { expect, test } from 'vitest'

import en from '@/i18n/locales/en.json'
import fr from '@/i18n/locales/fr.json'
import ja from '@/i18n/locales/ja.json'
import ru from '@/i18n/locales/ru.json'
import vi from '@/i18n/locales/vi.json'
import zhTW from '@/i18n/locales/zh-TW.json'
import zh from '@/i18n/locales/zh.json'

const sources = [
  '../preferences.tsx',
  '../outbox.tsx',
  '../../../system-settings/content/support-email-section.tsx',
]
const keys = new Set(
  sources.flatMap((source) =>
    [
      ...readFileSync(
        fileURLToPath(new URL(source, import.meta.url)),
        'utf8'
      ).matchAll(/\bt\(\s*'([^']+)'/g),
    ].map((match) => match[1])
  )
)
for (const key of [
  'Invalid support email settings',
  'Support email notification is no longer eligible',
  'Configure secure SMTP before enabling support email notifications',
  'A dashboard login session is required',
  'Enter up to five administrator email addresses.',
  'Enter an HTTPS origin without a path, query or fragment.',
]) {
  keys.add(key)
}

test.each(Object.entries({ en, zh, zhTW, fr, ja, ru, vi }))(
  '%s includes every support-mail label and backend error with unchanged placeholders',
  (_locale, bundle) => {
    const entries: Record<string, string> = bundle.translation
    for (const key of keys) {
      expect(entries[key], key).toBeTruthy()
      expect(entries[key].match(/{{[^}]+}}/g)?.sort() ?? [], key).toEqual(
        key.match(/{{[^}]+}}/g)?.sort() ?? []
      )
    }
  }
)
