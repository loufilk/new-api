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
import type { TopNavLink } from '@/components/layout/types'

export type NavigationExtensionContext = {
  isAuthenticated: boolean
  status: Record<string, unknown> | null
}

export type NavigationExtension = {
  id: string
  publicHomeUrl?: string
  quickstartDocsUrl?: string
  apply: (
    links: readonly TopNavLink[],
    context: NavigationExtensionContext
  ) => TopNavLink[]
}

// Compile-time extension list: this is intentionally static and immutable.
// Frontend extensions are composed during bundle construction, not hot-loaded
// at runtime, matching the backend extension registry's startup-only contract.
let navigationExtensions: readonly NavigationExtension[] = []

export function setNavigationExtensions(
  extensions: readonly NavigationExtension[]
) {
  if (navigationExtensions.length > 0) {
    throw new Error('frontend navigation extensions are already configured')
  }
  navigationExtensions = Object.freeze([...extensions])
}

export function applyNavigationExtensions(
  links: readonly TopNavLink[],
  context: NavigationExtensionContext
): TopNavLink[] {
  return navigationExtensions.reduce(
    (current, extension) => extension.apply(current, context),
    links.map((link) => ({ ...link }))
  )
}

export function extensionPublicHomeUrl(fallback = '/') {
  for (const extension of navigationExtensions) {
    if (extension.publicHomeUrl) return extension.publicHomeUrl
  }
  return fallback
}

export function extensionQuickstartDocsUrl(fallback = '/help') {
  for (const extension of navigationExtensions) {
    if (extension.quickstartDocsUrl) return extension.quickstartDocsUrl
  }
  return fallback
}
