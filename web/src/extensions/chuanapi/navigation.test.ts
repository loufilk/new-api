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
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, test } from 'vitest'

import type { TopNavLink } from '@/components/layout/types'

import {
  CHUANAPI_LEGACY_QUICKSTART_DOCS,
  CHUANAPI_PUBLIC_HOME,
  chuanapiNavigationExtension,
} from './navigation'

const webSrc = resolve(dirname(fileURLToPath(import.meta.url)), '../..')

function source(path: string) {
  return readFileSync(resolve(webSrc, path), 'utf8')
}

describe('ChuanAPI cross-shell navigation', () => {
  test('uses the canonical public homepage', () => {
    expect(CHUANAPI_PUBLIC_HOME).toBe('https://chuanapi.com/')
  })

  test('keeps the built-in docs fallback inside the SPA', () => {
    const links: TopNavLink[] = [{ title: 'Docs', href: '/help' }]
    expect(
      chuanapiNavigationExtension.apply(links, {
        isAuthenticated: false,
        status: null,
      })
    ).toEqual([
      {
        title: 'Docs',
        href: '/help',
        documentNavigation: false,
        external: false,
      },
    ])
  })

  test.each([
    'https://chuanapi.com/#quickstart',
    'https://chuanapi.com#quickstart',
    CHUANAPI_LEGACY_QUICKSTART_DOCS,
    'https://chuanapi.com/docs/quickstart',
  ])('routes the legacy docs entry %s to the built-in docs', (entry) => {
    const links: TopNavLink[] = [{ title: 'Docs', href: entry }]
    const applied = chuanapiNavigationExtension.apply(links, {
      isAuthenticated: false,
      status: null,
    })[0]

    expect(applied?.href).toBe('/help')
    expect(applied?.documentNavigation).toBe(false)
    expect(applied?.external).toBe(false)
  })

  test('leaves custom docs URLs untouched', () => {
    const links: TopNavLink[] = [
      { title: 'Docs', href: 'https://docs.example.com/start' },
    ]
    expect(
      chuanapiNavigationExtension.apply(links, {
        isAuthenticated: false,
        status: null,
      })[0]?.href
    ).toBe('https://docs.example.com/start')
  })

  test('marks Home as a same-tab document navigation', () => {
    const links = source('hooks/use-top-nav-links.ts')
    expect(links).toMatch(/href: '\/'/)
    expect(links).toMatch(/applyNavigationExtensions/)
  })

  test('keeps New API pricing navigation inside the SPA', () => {
    const links = source('hooks/use-top-nav-links.ts')
    const pricingBlock = links.match(
      /links\.push\(\{\s*title: t\('Model Square'\),[\s\S]*?\}\)/
    )?.[0]

    expect(pricingBlock).toContain("href: '/pricing'")
    expect(pricingBlock).not.toContain('documentNavigation')
  })

  test('renders pricing routes without self-reloading the document', () => {
    const pricingRoute = source('routes/pricing/index.tsx')
    const modelRoute = source('routes/pricing/$modelId/index.tsx')

    expect(pricingRoute).toMatch(/component: Pricing/)
    expect(modelRoute).toMatch(/component: ModelDetails/)
    expect(pricingRoute).not.toMatch(/reloadDocument/)
    expect(modelRoute).not.toMatch(/reloadDocument/)
  })

  test('renders the original authenticated Playground inside New API', () => {
    const playgroundRoute = source('routes/_authenticated/playground/index.tsx')

    expect(playgroundRoute).toMatch(
      /createFileRoute\('\/_authenticated\/playground\/'\)/
    )
    expect(playgroundRoute).toMatch(/component: PlaygroundPage/)
    expect(playgroundRoute).toMatch(/<Playground \/>/)
    expect(playgroundRoute).not.toMatch(/reloadDocument/)
  })

  test('brand links bypass the New API SPA router', () => {
    const brand = source('components/layout/components/system-brand.tsx')
    expect(brand).toMatch(/<a[\s\S]*href=\{extensionPublicHomeUrl\(\)\}/)
    expect(brand).not.toMatch(/<Link[\s\S]*extensionPublicHomeUrl/)
  })
})
