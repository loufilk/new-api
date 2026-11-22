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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, test } from 'vitest'

import '@/extensions/bootstrap'
import { extensionBrandFavicon, extensionBrandLogo } from '@/extensions'
import { AuthLayout } from '@/features/auth/auth-layout'
import { applyFaviconToDom } from '@/lib/dom-utils'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { HeaderLogo } from '../header-logo'

const initialConfig = useSystemConfigStore.getState()

beforeEach(() => {
  localStorage.clear()
  useSystemConfigStore.setState(initialConfig, true)
})

afterEach(() => {
  document.head
    .querySelectorAll('link[rel~="icon"]')
    .forEach((link) => link.remove())
  useSystemConfigStore.setState(initialConfig, true)
  localStorage.clear()
})

describe('shared brand presentation', () => {
  test('a cached logo cannot replace the deployment mark on the login layout', () => {
    useSystemConfigStore.setState({
      config: {
        ...initialConfig.config,
        systemName: 'WanChuan',
        logo: '/old-logo.png',
      },
      loadedLogoUrl: '/old-logo.png',
      loading: false,
    })
    const client = new QueryClient()
    const view = render(
      <QueryClientProvider client={client}>
        <AuthLayout>
          <div>Login form</div>
        </AuthLayout>
      </QueryClientProvider>
    )

    expect(screen.getByRole('img', { name: 'Logo' })).toHaveAttribute(
      'src',
      extensionBrandLogo()
    )
    expect(screen.getByRole('heading', { name: 'WanChuan' })).toBeVisible()
    expect(screen.getByText('Login form')).toBeVisible()
    view.unmount()
    client.clear()
  })

  test('a preloading header mark becomes visible only when loading finishes', () => {
    const view = render(<HeaderLogo alt='WanChuan' logoLoaded={false} />)
    expect(screen.getByRole('img', { name: 'WanChuan' })).toHaveClass(
      'opacity-0'
    )
    view.rerender(<HeaderLogo alt='WanChuan' logoLoaded />)
    expect(screen.getByRole('img', { name: 'WanChuan' })).toHaveClass(
      'opacity-100'
    )
  })

  test('cached and refreshed status logos keep one legible deployment favicon', () => {
    const old = document.createElement('link')
    old.rel = 'icon'
    old.href = '/old-logo.png'
    document.head.appendChild(old)

    applyFaviconToDom('/cached-logo.png')
    applyFaviconToDom('/refreshed-logo.png')

    const icons = document.head.querySelectorAll('link[rel~="icon"]')
    expect(icons).toHaveLength(1)
    expect(icons[0]).toHaveAttribute('href', extensionBrandFavicon(''))
  })
})
