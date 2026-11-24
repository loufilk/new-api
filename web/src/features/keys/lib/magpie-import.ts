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
import { sha256 } from '@noble/hashes/sha2.js'
import { bytesToHex } from '@noble/hashes/utils.js'

import { API_KEY_STATUS } from '../constants'
import type { ApiKey } from '../types'

export const MAGPIE_HELP_URL = 'https://usemagpie.ai/docs/import'

type MagpieProvider = Readonly<{
  id: string
  name: string
  chat: string
  responses: string
  anthropic: string
  gemini: string
}>

export function isMagpieEligibleKey(
  token: Pick<ApiKey, 'id' | 'status' | 'expired_time'>,
  now = Date.now()
): boolean {
  return (
    Number.isSafeInteger(token.id) &&
    token.id > 0 &&
    token.status === API_KEY_STATUS.ENABLED &&
    (token.expired_time === -1 ||
      (Number.isSafeInteger(token.expired_time) &&
        token.expired_time > Math.floor(now / 1000)))
  )
}

type BrowserPlatform = Pick<
  Navigator,
  'userAgent' | 'platform' | 'maxTouchPoints'
>

export function supportsMagpieDesktop(platform: BrowserPlatform): boolean {
  if (/Android|iPhone|iPad|iPod|CrOS/i.test(platform.userAgent)) return false
  if (platform.platform === 'MacIntel' && platform.maxTouchPoints > 1) {
    return false
  }
  return /Windows|Macintosh|Linux|X11/i.test(platform.userAgent)
}

// The only permitted input is the built API base (the same as the key-reveal
// API) or, for a same-origin build, the page origin. Never use cached status
// addresses, user-entered mirrors or a provider preset.
export function describeMagpieProvider(
  apiBase: string,
  tokenId: number
): MagpieProvider | null {
  if (!Number.isSafeInteger(tokenId) || tokenId <= 0) return null
  // Reject ambiguous URLs before the URL parser can clean up dot segments,
  // backslashes, controls or an escaped separator. Mount prefixes are intact.
  const parts = /^(https?):\/\/([^/?#\\]+)(\/[A-Za-z0-9._~/-]*)?$/i.exec(
    apiBase
  )
  if (!parts || [...apiBase].some((char) => char.charCodeAt(0) <= 32)) {
    return null
  }
  const rawPrefix = (parts[3] ?? '').replace(/\/+$/, '')
  if (rawPrefix.split('/').some((part) => part === '.' || part === '..')) {
    return null
  }
  if (rawPrefix.includes('//') || /\/v1(?:beta)?$/i.test(rawPrefix)) return null
  if (parts[2].includes('@') || parts[2].includes('%')) return null

  let address: URL
  try {
    address = new URL(apiBase)
  } catch {
    return null
  }
  const loopback = ['localhost', '127.0.0.1', '[::1]'].includes(
    address.hostname
  )
  if (
    address.protocol !== 'https:' &&
    !(address.protocol === 'http:' && loopback)
  ) {
    return null
  }
  if (address.username || address.password || address.search || address.hash) {
    return null
  }

  const root = address.origin + rawPrefix
  const fingerprint = bytesToHex(sha256(new TextEncoder().encode(root))).slice(
    0,
    24
  )
  return {
    id: `new-api-${tokenId}-${fingerprint}`,
    name: `New API (Key ${tokenId})`,
    chat: `${root}/v1`,
    responses: `${root}/v1`,
    anthropic: root,
    gemini: `${root}/v1beta`,
  }
}

// Keep the key off HTTPS URLs, anchors, caches and clipboard. The result exists
// only inside the explicit desktop-launch event. No model list/catalog: Magpie
// queries the same API using the imported Key and New API scopes discovery.
export function magpieImportUrl(
  provider: MagpieProvider,
  key: string
): string | null {
  if (
    !key.startsWith('sk-') ||
    key.length <= 3 ||
    key.length > 4096 ||
    new TextEncoder().encode(key).byteLength > 4096 ||
    [...key].some(
      (char) => char.charCodeAt(0) <= 32 || char.charCodeAt(0) === 127
    )
  ) {
    return null
  }

  return `magpie://import?${new URLSearchParams({ ...provider, key }).toString()}`
}
