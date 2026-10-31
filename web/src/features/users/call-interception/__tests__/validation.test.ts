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

import { callInterceptionSchema } from '../lib/schema'

describe('interception reply validation', () => {
  it.each(['', ' \n\t ', '\u3000', '\u200b\u200d', '\u0000'])(
    'rejects blank enabled content %j',
    (reply) => {
      expect(
        callInterceptionSchema.safeParse({ enabled: true, reply }).success
      ).toBe(false)
    }
  )
  it.each([false, true])(
    'rejects NUL in a reply when enabled=%s',
    (enabled) => {
      const result = callInterceptionSchema.safeParse({
        enabled,
        reply: 'Visible text\u0000',
      })
      expect(result.success).toBe(false)
      if (!result.success) {
        expect(result.error.issues[0].message).toBe(
          'Custom reply must not contain null characters'
        )
      }
    }
  )
  it('allows an empty disabled reply', () => {
    expect(
      callInterceptionSchema.safeParse({ enabled: false, reply: '' }).success
    ).toBe(true)
  })
  it('accepts exactly 16000 UTF-8 bytes and rejects the next byte', () => {
    const reply = `${'汉'.repeat(5333)}a`
    expect(
      callInterceptionSchema.safeParse({ enabled: true, reply }).success
    ).toBe(true)
    expect(
      callInterceptionSchema.safeParse({ enabled: true, reply: `${reply}a` })
        .success
    ).toBe(false)
  })
})
