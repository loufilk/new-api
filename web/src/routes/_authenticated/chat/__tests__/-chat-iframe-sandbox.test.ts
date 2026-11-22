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

import { CHAT_IFRAME_SANDBOX } from '../$chatId'

describe('chat iframe sandbox contract', () => {
  test('provides a sandbox that allows the chat app to run', () => {
    const tokens = new Set(CHAT_IFRAME_SANDBOX.split(/\s+/).filter(Boolean))

    expect(tokens.has('allow-scripts')).toBe(true)
    expect(tokens.has('allow-forms')).toBe(true)
  })

  test('never combines allow-scripts with allow-same-origin', () => {
    const tokens = new Set(CHAT_IFRAME_SANDBOX.split(/\s+/).filter(Boolean))

    expect(tokens.has('allow-scripts') && tokens.has('allow-same-origin')).toBe(
      false
    )
  })
})
