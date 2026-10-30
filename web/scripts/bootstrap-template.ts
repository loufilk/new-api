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
import { join } from 'node:path'

import { convertDetectedLanguage } from '../src/i18n/languages'

/** The HTML shell must work before React or the async locale backend loads. */
export function getBootstrapTemplateParameters(
  webRoot: string
): Record<string, string> {
  const messages = Object.fromEntries(
    ['en', 'zh', 'zh-TW', 'fr', 'ru', 'ja', 'vi'].map((locale) => {
      const resource = JSON.parse(
        readFileSync(
          join(webRoot, 'src/i18n/locales', `${locale}.json`),
          'utf8'
        )
      ) as { translation: Record<string, string> }
      const key = convertDetectedLanguage(locale).toLowerCase()
      return [
        key,
        {
          loading: resource.translation['Loading...'] || 'Loading...',
          failed:
            resource.translation['Please try again later.'] ||
            'Please try again later.',
          retry: resource.translation.Retry || 'Retry',
        },
      ]
    })
  )
  return {
    bootstrapMessages: JSON.stringify(messages).replaceAll('<', '\\u003c'),
    bootstrapLanguage: convertDetectedLanguage.toString(),
  }
}
