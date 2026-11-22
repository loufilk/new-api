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
// Only a deadline is persisted. A hard reload must not repeat a rate-limited
// refresh, but no token, identity, or authorization verdict belongs in storage.
const storageKey = 'new-api:auth-refresh-retry-at'
// Bound a bad proxy/header or storage value. Even after a week a manual retry
// still has to pass the real server-side limiter and session verification.
const maxBackoff = 7 * 24 * 60 * 60 * 1000
let retryAt = 0

export class AuthRefreshUnavailable extends Error {
  constructor(readonly retryAt: number) {
    super('Session verification temporarily unavailable')
    this.name = 'AuthRefreshUnavailable'
  }
}

export function refreshRetryDeadline(
  status: number,
  retryAfter?: unknown
): number {
  const now = Date.now()
  const value = typeof retryAfter === 'string' ? retryAfter.trim() : ''
  if (value) {
    const deadline = /^\d+$/.test(value)
      ? now + Number(value) * 1000
      : Date.parse(value)
    if (Number.isSafeInteger(deadline)) {
      return Math.max(now + 1000, Math.min(deadline, now + maxBackoff))
    }
  }
  // Proxies and cross-origin responses do not always expose Retry-After.
  return now + (status === 429 ? 30_000 : 3_000)
}

export function setRefreshRetryAt(deadline: number): void {
  retryAt = deadline
  try {
    if (deadline) sessionStorage.setItem(storageKey, String(deadline))
    else sessionStorage.removeItem(storageKey)
  } catch {
    // Storage may be disabled. In-memory coalescing still works in this page.
  }
}

export function getRefreshRetryAt(): number {
  try {
    const stored = Number(sessionStorage.getItem(storageKey))
    if (Number.isSafeInteger(stored) && stored <= Date.now() + maxBackoff) {
      retryAt = Math.max(retryAt, stored)
    }
  } catch {
    // Use the in-memory deadline.
  }
  return retryAt > Date.now() ? retryAt : 0
}
