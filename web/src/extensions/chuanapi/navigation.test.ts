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
import { ROLE } from '@/lib/roles'

import {
  CHUANAPI_LEGACY_QUICKSTART_DOCS,
  CHUANAPI_PUBLIC_HOME,
  chuanapiNavigationExtension,
  chuanapiSidebarNavigationExtension,
  chuanapiToolsNavigationExtension,
} from './navigation'

const webSrc = resolve(dirname(fileURLToPath(import.meta.url)), '../..')

function source(path: string) {
  return readFileSync(resolve(webSrc, path), 'utf8')
}

describe('ChuanAPI cross-shell navigation', () => {
  test('shows partner applications to signed-in users and management to admins', () => {
    const sidebar = {
      navGroups: [
        { id: 'personal', title: 'Personal', items: [] },
        { id: 'admin', title: 'Admin', items: [] },
      ],
    }
    const ordinary = chuanapiSidebarNavigationExtension.apply(sidebar, {
      isAuthenticated: true,
      role: ROLE.USER,
    })
    expect(ordinary.navGroups[0]?.items).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          title: 'Partner application',
          url: '/partner-applications',
        }),
      ])
    )
    expect(ordinary.navGroups[1]?.items).toHaveLength(0)

    const admin = chuanapiSidebarNavigationExtension.apply(sidebar, {
      isAuthenticated: true,
      role: ROLE.ADMIN,
      user: {
        id: 12,
        username: 'reader',
        role: ROLE.ADMIN,
        permissions: {
          admin_permissions: { partner_applications: { read: true } },
        },
      },
    })
    expect(admin.navGroups[1]?.items).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          title: 'Partner applications',
          url: '/partner-application-management',
          requiredRole: ROLE.ADMIN,
        }),
      ])
    )

    const deniedAdmin = chuanapiSidebarNavigationExtension.apply(sidebar, {
      isAuthenticated: true,
      role: ROLE.ADMIN,
      user: {
        id: 13,
        username: 'no-supplier-access',
        role: ROLE.ADMIN,
        permissions: {
          admin_permissions: {
            partner_applications: { read: false, review: true },
          },
        },
      },
    })
    expect(deniedAdmin.navGroups[1]?.items).toHaveLength(0)

    const settingsReader = chuanapiSidebarNavigationExtension.apply(sidebar, {
      isAuthenticated: true,
      role: ROLE.ADMIN,
      user: {
        id: 14,
        username: 'settings-reader',
        role: ROLE.ADMIN,
        permissions: {
          admin_permissions: { partner_settings: { read: true } },
        },
      },
    })
    expect(settingsReader.navGroups[1]?.items).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ url: '/partner-application-management' }),
      ])
    )

    const root = chuanapiSidebarNavigationExtension.apply(sidebar, {
      isAuthenticated: true,
      role: ROLE.SUPER_ADMIN,
      user: { id: 1, username: 'root', role: ROLE.SUPER_ADMIN },
    })
    expect(root.navGroups[1]?.items).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          title: 'Partner applications',
          url: '/partner-application-management',
          requiredRole: ROLE.ADMIN,
        }),
      ])
    )
  })

  test('does not add authenticated links for signed-out navigation', () => {
    const sidebar = {
      navGroups: [
        { id: 'personal', title: 'Personal', items: [] },
        { id: 'admin', title: 'Admin', items: [] },
      ],
    }
    expect(
      chuanapiSidebarNavigationExtension.apply(sidebar, {
        isAuthenticated: false,
      })
    ).toEqual(sidebar)
  })

  test('offers the guarded partner alias without overriding configured navigation', () => {
    const context = { isAuthenticated: false, status: null }
    const docs = { title: 'Docs', href: '/help' }
    const consoleLink = { title: 'Console', href: '/dashboard' }
    const existingPartner = {
      title: 'Configured applicant page',
      href: '/partner-applications',
    }
    expect(
      chuanapiToolsNavigationExtension.apply([docs, consoleLink], context)
    ).toContainEqual({
      title: 'Partner application',
      href: '/partner',
    })
    expect(
      chuanapiToolsNavigationExtension
        .apply([docs], context)
        .some((link) => link.href === '/partner')
    ).toBe(false)
    const personalized = chuanapiToolsNavigationExtension.apply(
      [docs, consoleLink, existingPartner],
      context
    )
    expect(personalized).toContainEqual(existingPartner)
    expect(personalized.some((link) => link.href === '/partner')).toBe(false)
  })

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
