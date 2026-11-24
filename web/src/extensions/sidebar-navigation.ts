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
import type { SidebarData } from '@/components/layout/types'
import type { AuthUser } from '@/stores/auth-store'

export type SidebarNavigationContext = {
  isAuthenticated: boolean
  role?: number
  user?: AuthUser | null
}

export type SidebarNavigationExtension = {
  id: string
  apply: (
    sidebar: SidebarData,
    context: SidebarNavigationContext
  ) => SidebarData
}

let sidebarNavigationExtensions: readonly SidebarNavigationExtension[] = []

export function setSidebarNavigationExtensions(
  extensions: readonly SidebarNavigationExtension[]
) {
  if (sidebarNavigationExtensions.length > 0) {
    throw new Error(
      'frontend sidebar navigation extensions are already configured'
    )
  }
  sidebarNavigationExtensions = Object.freeze([...extensions])
}

export function applySidebarNavigationExtensions(
  sidebar: SidebarData,
  context: SidebarNavigationContext
): SidebarData {
  return sidebarNavigationExtensions.reduce(
    (current, extension) => extension.apply(current, context),
    sidebar
  )
}
