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
import { afterEach, describe, expect, test } from 'vitest'

import { useAuthStore, type AuthBundle } from '@/stores/auth-store'

import { applyAuthBundle, clearAuthentication } from '../auth-session'
import {
  clearSessionHint,
  hasSessionHint,
  markSessionHint,
  readCookie,
  SESSION_HINT_COOKIE_NAME,
} from '../session-hint'

const FUTURE_EXPIRES_AT = Math.floor(Date.now() / 1000) + 30 * 24 * 60 * 60

function bundleWithExpiry(expiresAt: number): AuthBundle {
  return {
    access_token: 'access-token',
    token_type: 'Bearer',
    access_expires_at: Math.floor(Date.now() / 1000) + 600,
    user: { id: 7, username: 'hint-user', role: 1 },
    session: {
      sid: 'session-hint',
      current: true,
      login_method: 'password',
      ip: '127.0.0.1',
      user_agent: 'test',
      created_at: 100,
      last_active_at: 100,
      expires_at: expiresAt,
    },
  }
}

const bundle = bundleWithExpiry(FUTURE_EXPIRES_AT)

let restoreDocumentCookie: (() => void) | null = null

function captureCookieWrites(): string[] {
  const writes: string[] = []
  const descriptor = Object.getOwnPropertyDescriptor(
    Document.prototype,
    'cookie'
  )
  Object.defineProperty(document, 'cookie', {
    configurable: true,
    get: () => (descriptor?.get ? descriptor.get.call(document) : ''),
    set: (value: string) => {
      writes.push(value)
      descriptor?.set?.call(document, value)
    },
  })
  restoreDocumentCookie = () => {
    if (descriptor) Object.defineProperty(document, 'cookie', descriptor)
  }
  return writes
}

afterEach(() => {
  restoreDocumentCookie?.()
  restoreDocumentCookie = null
  clearSessionHint()
  useAuthStore.getState().auth.reset('idle')
})

describe('frontend session hint', () => {
  test('marking the hint makes it readable on the frontend host', () => {
    expect(hasSessionHint()).toBe(false)

    markSessionHint(FUTURE_EXPIRES_AT)

    expect(hasSessionHint()).toBe(true)
    expect(readCookie(document.cookie, SESSION_HINT_COOKIE_NAME)).toBe('1')
  })

  test('the hint carries the remaining server session TTL, not a fixed lifetime', () => {
    const before = Math.floor(Date.now() / 1000)
    const writes = captureCookieWrites()

    markSessionHint(before + 120)

    const maxAge = Number(/Max-Age=(\d+)/.exec(writes[0] ?? '')?.[1])
    expect(maxAge).toBeGreaterThanOrEqual(119)
    expect(maxAge).toBeLessThanOrEqual(120)
  })

  test('an already expired session clears the hint instead of writing one', () => {
    markSessionHint(FUTURE_EXPIRES_AT)
    expect(hasSessionHint()).toBe(true)

    const writes = captureCookieWrites()
    markSessionHint(Math.floor(Date.now() / 1000) - 1)

    expect(hasSessionHint()).toBe(false)
    expect(writes[0]).toContain(`${SESSION_HINT_COOKIE_NAME}=;`)
    expect(writes[0]).toContain('Max-Age=0')
  })

  test('clearing the hint removes it without touching credentials', () => {
    markSessionHint(FUTURE_EXPIRES_AT)
    clearSessionHint()

    expect(hasSessionHint()).toBe(false)
  })

  test('a successful authentication result marks the hint with the session expiry', () => {
    applyAuthBundle(bundle, false)

    expect(hasSessionHint()).toBe(true)
  })

  test('an authentication result whose session already expired does not mark the hint', () => {
    applyAuthBundle(bundleWithExpiry(Math.floor(Date.now() / 1000) - 1), false)

    expect(hasSessionHint()).toBe(false)
  })

  test('explicit sign-out clears the hint', () => {
    applyAuthBundle(bundle, false)

    clearAuthentication(false)

    expect(hasSessionHint()).toBe(false)
    expect(useAuthStore.getState().auth.user).toBe(null)
  })
})
