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
import { readFileSync, readdirSync } from 'node:fs'
import path from 'node:path'

import { describe, expect, test } from 'vitest'

const root = path.resolve('src/features/agent-platform')
function sourceFiles(directory: string): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    if (entry.name === '__tests__') return []
    const file = path.join(directory, entry.name)
    if (entry.isDirectory()) return sourceFiles(file)
    return /\.tsx?$/.test(entry.name) ? [file] : []
  })
}
function uiKeys(): string[] {
  const keys = new Set<string>()
  for (const file of sourceFiles(root)) {
    const source = readFileSync(file, 'utf8').replace(/^\/\*[\s\S]*?\*\//, '')
    const decoded = (value: string) =>
      value.replaceAll("\\'", "'").replaceAll('\\"', '"')
    for (const match of source.matchAll(
      /\bt\(\s*(['"])((?:\\.|(?!\1)[^\\])*)\1/g
    )) {
      keys.add(decoded(match[2]))
    }
    for (const match of source.matchAll(
      /(?:label|description):\s*(['"])((?:\\.|(?!\1)[^\\])*)\1/g
    )) {
      keys.add(decoded(match[2]))
    }
    // Include dynamic English source labels, while keeping each quoted literal separate.
    for (const match of source.matchAll(/(['"])((?:\\.|(?!\1).)*)\1/g)) {
      const value = decoded(match[2])
      if (/^[A-Z]/.test(value) && value.includes(' ')) keys.add(value)
    }
  }
  return [...keys]
}

const locales = ['en', 'zh', 'zh-TW', 'fr', 'ja', 'ru', 'vi']
const english = JSON.parse(readFileSync('src/i18n/locales/en.json', 'utf8'))
  .translation as Record<string, string>
describe('agent platform seven-language coverage', () => {
  test.each(locales)(
    '%s includes all static and schema-driven business copy with unchanged placeholders',
    (locale) => {
      const values = JSON.parse(
        readFileSync(`src/i18n/locales/${locale}.json`, 'utf8')
      ).translation as Record<string, string>
      const missing = uiKeys().filter((key) => !values[key])
      expect(missing).toEqual([])
      for (const key of uiKeys()) {
        const placeholders = (value: string) =>
          [...value.matchAll(/\{\{([^}]+)\}\}/g)]
            .map((match) => match[1])
            .sort()
        expect(placeholders(values[key]), key).toEqual(
          placeholders(english[key])
        )
      }
    }
  )
  test.each(locales.filter((locale) => locale !== 'en'))(
    '%s does not fall back to English for critical financial safeguards',
    (locale) => {
      const values = JSON.parse(
        readFileSync(`src/i18n/locales/${locale}.json`, 'utf8')
      ).translation as Record<string, string>
      for (const key of [
        'A timeout is not a failed payment. Unknown results remain frozen and must be reconciled with the same business number; never submit a replacement payout.',
        'Claiming binds the quota to your account but does not redeem it. You must confirm redemption separately. Your existing invitation attribution does not change.',
        'Sales freeze, settlement freeze and user-account suspension are different controls. Exited agents retain historical records and customer delivery obligations.',
        'Manual processing only. Keep this business number for submission and reconciliation. An unknown payment must never be sent again.',
      ]) {
        expect(values[key], key).toBeTruthy()
        expect(values[key], key).not.toBe(english[key])
      }
    }
  )
})
