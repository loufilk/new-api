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
import {
  extensionPublicHomeUrl,
  extensionQuickstartDocsUrl,
} from '@/extensions'

export const MARKETING_HOME_URL = extensionPublicHomeUrl('/').replace(/\/$/, '')

/** Built-in New API documentation route that lives inside the SPA. */
export const BUILT_IN_HELP_PATH = '/help'

export const QUICKSTART_DOCS_URL =
  extensionQuickstartDocsUrl(BUILT_IN_HELP_PATH)

/**
 * Documentation entries that pointed at the marketing web home before the
 * built-in docs were restored. They must keep working, but they resolve to
 * the built-in `/help` documentation instead of leaving the SPA.
 */
const LEGACY_PUBLIC_DOCS_URLS = new Set([
  `${MARKETING_HOME_URL}/#quickstart`,
  `${MARKETING_HOME_URL}#quickstart`,
  `${MARKETING_HOME_URL}/docs/quickstart`,
  `${MARKETING_HOME_URL}/docs/quickstart/`,
])

export function resolvePublicDocsUrl(value: unknown): string {
  if (typeof value !== 'string') return QUICKSTART_DOCS_URL

  const normalized = value.trim()
  if (!normalized || LEGACY_PUBLIC_DOCS_URLS.has(normalized)) {
    return QUICKSTART_DOCS_URL
  }

  return normalized
}

export function isPublicRegistrationEnabled(
  status: Record<string, unknown> | null
): boolean {
  return status?.register_enabled === true
}
