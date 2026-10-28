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
 * Generic brand extension contract.
 *
 * Core components must resolve brand assets through the generic accessors in
 * `@/extensions`, never by importing a concrete extension module. The concrete
 * extension is composed once in `extensions/bootstrap.ts` at bundle startup.
 */
export type BrandExtension = {
  id: string
  logoUrl?: string
  logoClassName?: string
  faviconUrl?: string
}

let brandExtension: BrandExtension | null = null

export function setBrandExtension(extension: BrandExtension) {
  if (brandExtension) {
    throw new Error('frontend brand extension is already configured')
  }
  brandExtension = Object.freeze({ ...extension })
}

export function extensionBrandLogo(fallback = '/logo.png') {
  return brandExtension?.logoUrl ?? fallback
}

export function extensionBrandLogoClassName() {
  return brandExtension?.logoClassName
}

export function extensionBrandFavicon(fallback: string) {
  return brandExtension?.faviconUrl ?? fallback
}
