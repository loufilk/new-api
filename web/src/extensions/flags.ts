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
 * Generic extension feature flags.
 *
 * Core code gates extension-owned UI through `extensionFlags` /
 * `isExtensionFeatureEnabled` instead of importing a concrete extension's
 * flag constants. Flags default to disabled and can only be configured once
 * during bundle composition in `extensions/bootstrap.ts`.
 */
export type ExtensionFeatureFlag = 'showLegacyAffiliateQuota'

export type ExtensionFlags = Record<ExtensionFeatureFlag, boolean>

const extensionFlags: ExtensionFlags = {
  showLegacyAffiliateQuota: false,
}

const configuredFlags = new Set<ExtensionFeatureFlag>()

export function setExtensionFlags(incoming: Partial<ExtensionFlags>) {
  const entries = (Object.keys(incoming) as ExtensionFeatureFlag[]).filter(
    (key) => incoming[key] !== undefined
  )
  for (const key of entries) {
    if (configuredFlags.has(key)) {
      throw new Error(`frontend extension flag is already configured: ${key}`)
    }
  }
  for (const key of entries) {
    extensionFlags[key] = incoming[key] as boolean
    configuredFlags.add(key)
  }
}

export function isExtensionFeatureEnabled(flag: ExtensionFeatureFlag): boolean {
  return extensionFlags[flag]
}

export { extensionFlags }
