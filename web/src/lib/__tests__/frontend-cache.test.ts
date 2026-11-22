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
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { initializeFrontendCache } from '../frontend-cache'

beforeEach(() => localStorage.clear())

describe('frontend cache migration', () => {
  it.each([null, 'older-cache-version'])(
    'preserves user-owned and unrelated data when the version is %s',
    (version) => {
      const saved = {
        'playground_messages:42': '[{"content":"saved conversation"}]',
        'playground_config:42': '{"model":"example"}',
        'image-generation-history:v1:user:42': '[{"id":"saved-image"}]',
        'newapi:theme:v1:mode': 'dark',
        user: '{"id":42}',
        uid: '42',
        'another-app:draft': 'unfinished work',
      }
      for (const [key, value] of Object.entries(saved)) {
        localStorage.setItem(key, value)
      }
      if (version) localStorage.setItem('newapi:default:cache-version', version)
      localStorage.setItem('status', '{"system_name":"stale"}')
      localStorage.setItem('status:updatedAt', '1')
      localStorage.setItem('home_page_content', '<p>stale</p>')

      initializeFrontendCache()

      for (const [key, value] of Object.entries(saved)) {
        expect(localStorage.getItem(key)).toBe(value)
      }
      expect(localStorage.getItem('status')).toBeNull()
      expect(localStorage.getItem('status:updatedAt')).toBeNull()
      expect(localStorage.getItem('home_page_content')).toBeNull()
      expect(localStorage.getItem('newapi:default:cache-version')).toBe(
        'default-v1'
      )
      localStorage.setItem('status', 'fresh')
      initializeFrontendCache()
      expect(localStorage.getItem('status')).toBe('fresh')
    }
  )

  it('still boots when storage is unavailable', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new DOMException('Storage unavailable', 'SecurityError')
    })
    expect(() => initializeFrontendCache()).not.toThrow()
  })
})
