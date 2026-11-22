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
/**
 * Detection of the server's login-session hint cookie.
 *
 * The Refresh Cookie is `HttpOnly` and scoped to `/api/user/auth`, so a page at
 * `/` cannot read it and cannot tell an anonymous visitor from a returning one.
 * The server therefore writes `new_api_has_session=1` alongside it — same
 * expiry, `Path=/`, not `HttpOnly` — purely so the frontend can skip a refresh
 * that is certain to fail.
 *
 * This is an optimization, never an authorization signal. A present hint means
 * "a Refresh Cookie was issued at some point"; it can be stale after a
 * server-side revocation, and it can be absent while a usable Refresh Cookie
 * still exists (a visitor who cleared site data for `/` only, or any session
 * created before this cookie shipped). Callers must treat a missing hint as
 * "not worth a request right now", not as "signed out", and must still be able
 * to reach the server when authentication actually matters.
 */
export const SESSION_HINT_COOKIE_NAME = 'new_api_has_session'

function writeCookie(name: string, value: string, maxAgeSeconds: number): void {
  if (typeof document === 'undefined') return
  const secure =
    typeof location !== 'undefined' && location.protocol === 'https:'
      ? '; Secure'
      : ''
  document.cookie = `${name}=${value}; Path=/; Max-Age=${maxAgeSeconds}; SameSite=Lax${secure}`
}

/**
 * Record the non-credential hint on the frontend host until the server session
 * itself expires.
 *
 * `sessionExpiresAt` is the login session's absolute expiry in Unix seconds.
 * The hint must expire with that deadline instead of a fixed lifetime:
 * authentication refreshes do not extend the server session, so a hint that
 * outlives it would announce a session that no longer exists. An expiry that is
 * already in the past clears the hint instead of writing one.
 *
 * The server's own hint is scoped to the API host, which is unreadable from a
 * separately hosted console. This host-scoped copy lets the console skip a
 * refresh that is certain to fail, exactly like the server-written one. It
 * carries no credential and must never be used as an authorization signal.
 */
export function markSessionHint(sessionExpiresAt: number): void {
  if (!Number.isFinite(sessionExpiresAt)) return
  const remainingSeconds = Math.floor(sessionExpiresAt - Date.now() / 1000)
  if (remainingSeconds <= 0) {
    clearSessionHint()
    return
  }
  writeCookie(SESSION_HINT_COOKIE_NAME, '1', remainingSeconds)
}

/** Remove the host-scoped hint on explicit sign-out or invalidation. */
export function clearSessionHint(): void {
  writeCookie(SESSION_HINT_COOKIE_NAME, '', 0)
}

/** Read a cookie value out of a `document.cookie`-shaped string. */
export function readCookie(cookieHeader: string, name: string): string | null {
  for (const part of cookieHeader.split(';')) {
    const separator = part.indexOf('=')
    if (separator < 0) continue
    if (part.slice(0, separator).trim() !== name) continue
    return part.slice(separator + 1).trim()
  }
  return null
}

/**
 * Whether the server currently claims a login session exists.
 *
 * Returns `true` when the hint cannot be read at all (no `document`, as in SSR
 * or a non-DOM test environment). An unreadable hint is not evidence of
 * absence, and the safe direction is to let the refresh proceed.
 */
export function hasSessionHint(): boolean {
  if (typeof document === 'undefined') return true
  return readCookie(document.cookie, SESSION_HINT_COOKIE_NAME) !== null
}
