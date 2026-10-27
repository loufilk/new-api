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
import { describe, expect, it } from 'vitest'

import { buildValidatedCCSwitchURL } from '@/features/keys/components/dialogs/cc-switch-utils'

import { clientApiBase, isSafeClientLink } from '../client-import'

describe('client import endpoints', () => {
  it.each([
    ['https://api.example.com/v1/', 'https://api.example.com'],
    ['https://api.example.com/gateway/v1', 'https://api.example.com/gateway'],
    ['https://api.example.com/', 'https://api.example.com'],
    ['http://localhost:3000/v1', 'http://localhost:3000'],
    ['javascript:alert(1)', null],
    ['https://name:secret@example.com', null],
    ['https://api.example.com?key=secret', null],
    ['http://api.example.com', null],
  ])('normalizes %s to a safe protocol base', (input, expected) =>
    expect(clientApiBase(input)).toBe(expected)
  )

  it('uses the chosen endpoint for CC Switch and appends v1 only for Codex', () => {
    const url = buildValidatedCCSwitchURL(
      'codex',
      'Work',
      { model: 'gpt-5' },
      'fixture-only',
      ['gpt-5'],
      'https://asia.example.com/proxy'
    )
    expect(new URL(url ?? '').searchParams.get('endpoint')).toBe(
      'https://asia.example.com/proxy/v1'
    )
    expect(new URL(url ?? '').searchParams.get('apiKey')).toBe(
      'sk-fixture-only'
    )
  })

  it.each([
    'javascript:alert(1)',
    'data:text/html,test',
    'file:///tmp/app',
    'httpsx://client',
  ])('rejects executable or invalid links: %s', (url) =>
    expect(isSafeClientLink(url)).toBe(false)
  )
})
