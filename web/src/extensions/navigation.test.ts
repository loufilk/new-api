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
import { describe, expect, test } from 'vitest'

import '@/extensions/bootstrap'
import {
  applyNavigationExtensions,
  extensionPublicHomeUrl,
  extensionQuickstartDocsUrl,
} from '@/extensions'

describe('frontend extension navigation contract', () => {
  test('applies ChuanAPI cross-shell home navigation without mutating input', () => {
    const links = [
      { title: 'Home', href: '/' },
      { title: 'Pricing', href: '/pricing' },
    ]

    const result = applyNavigationExtensions(links, {
      isAuthenticated: false,
      status: null,
    })

    expect(result).toEqual([
      {
        title: 'Home',
        href: 'https://chuanapi.com/',
        documentNavigation: true,
      },
      { title: 'Pricing', href: '/pricing' },
      { title: 'monitor.title', href: '/model-monitor' },
    ])
    expect(links).toEqual([
      { title: 'Home', href: '/' },
      { title: 'Pricing', href: '/pricing' },
    ])
  })

  test('publishes extension-owned public URLs with the built-in docs fallback', () => {
    expect(extensionPublicHomeUrl()).toBe('https://chuanapi.com/')
    expect(extensionQuickstartDocsUrl()).toBe('/help')
  })

  test('keeps the built-in docs entry inside the SPA after composition', () => {
    const result = applyNavigationExtensions(
      [{ title: 'Docs', href: '/help' }],
      {
        isAuthenticated: false,
        status: null,
      }
    )

    expect(result).toEqual([
      { title: 'monitor.title', href: '/model-monitor' },
      {
        title: 'Docs',
        href: '/help',
        documentNavigation: false,
        external: false,
      },
    ])
  })
})
