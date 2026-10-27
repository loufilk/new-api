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
import { API_KEY_STATUS } from '@/features/keys/constants'
import type { ApiKey } from '@/features/keys/types'

/** Metadata is checked again immediately before revealing the selected key. */
export function isImportableKey(token: ApiKey): boolean {
  return (
    token.status === API_KEY_STATUS.ENABLED &&
    (token.expired_time === -1 || token.expired_time > Date.now() / 1000) &&
    (token.unlimited_quota || token.remain_quota > 0)
  )
}

/** Clients add their protocol path; configured /v1 addresses must not duplicate it. */
export function clientApiBase(address: string): string | null {
  try {
    const url = new URL(address)
    const local = ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname)
    if (url.protocol !== 'https:' && !(local && url.protocol === 'http:')) {
      return null
    }
    if (url.username || url.password || url.search || url.hash) return null
    return `${url.origin}${url.pathname.replace(/\/+$/, '').replace(/\/v1$/, '')}`
  } catch {
    return null
  }
}

export function isCCSwitchPreset(url: string): boolean {
  return /^ccswitch(?::\/\/.*)?$/i.test(url.trim())
}

export function isSafeClientLink(url: string): boolean {
  return /^(https:\/\/|http:\/\/(?:localhost|127\.0\.0\.1)(?=[:/])|cherrystudio:\/\/|aionui:\/\/|deepchat:\/\/|aqbot:\/\/|ama:\/\/|opencat:\/\/|ccswitch:\/\/)/i.test(
    url
  )
}
