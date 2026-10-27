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
import { renderHook } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, test } from 'vitest'

import '@/extensions/bootstrap'
import type { TopNavLink } from '@/components/layout/types'
import { useTopNavLinks } from '@/hooks/use-top-nav-links'
import { STATUS_QUERY_KEY } from '@/lib/status-query'

/**
 * The Docs entry is the only navigation link whose destination is
 * configuration-driven. It must resolve to the built-in SPA documentation for
 * every legacy marketing value, and only an explicit external URL may leave
 * the SPA with a document request.
 */

const queryClients: QueryClient[] = []

function renderNavLinks(status: Record<string, unknown>) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  queryClients.push(queryClient)
  queryClient.setQueryData(STATUS_QUERY_KEY, status)

  function Wrapper(props: { children: ReactNode }) {
    return (
      <QueryClientProvider client={queryClient}>
        {props.children}
      </QueryClientProvider>
    )
  }

  return renderHook(() => useTopNavLinks(), { wrapper: Wrapper })
}

function findLink(links: TopNavLink[], title: string) {
  return links.find((link) => link.title === title)
}

afterEach(() => {
  queryClients.splice(0).forEach((client) => client.clear())
})

describe('top navigation docs entry', () => {
  test('a blank docs configuration falls back to the built-in /help SPA route', () => {
    const { result } = renderNavLinks({})

    const docs = findLink(result.current, 'Docs')
    expect(docs?.href).toBe('/help')
    expect(docs?.documentNavigation).toBe(false)
    expect(docs?.external).toBe(false)
  })

  test('a configured /help path stays inside the SPA', () => {
    const { result } = renderNavLinks({ docs_link: '/help' })

    const docs = findLink(result.current, 'Docs')
    expect(docs?.href).toBe('/help')
    expect(docs?.documentNavigation).toBe(false)
    expect(docs?.external).toBe(false)
  })

  test.each([
    'https://chuanapi.com/docs/quickstart/',
    'https://chuanapi.com/docs/quickstart',
    'https://chuanapi.com/#quickstart',
    'https://chuanapi.com#quickstart',
  ])(
    'the legacy docs entry %s routes to the built-in docs',
    (legacyDocsEntry) => {
      const { result } = renderNavLinks({ docs_link: legacyDocsEntry })

      const docs = findLink(result.current, 'Docs')
      expect(docs?.href).toBe('/help')
      expect(docs?.documentNavigation).toBe(false)
      expect(docs?.external).toBe(false)
    }
  )

  test('an explicit external docs URL keeps its same-tab document navigation', () => {
    const { result } = renderNavLinks({
      docs_link: 'https://docs.example.com/guide',
    })

    const docs = findLink(result.current, 'Docs')
    expect(docs?.href).toBe('https://docs.example.com/guide')
    expect(docs?.documentNavigation).toBe(true)
    expect(docs?.external).toBe(false)
  })

  test('API access remains a separate internal destination alongside custom documentation', () => {
    const { result } = renderNavLinks({
      docs_link: 'https://docs.example.com/guide',
    })
    expect(findLink(result.current, 'API Access')?.href).toBe('/help/api')
    expect(findLink(result.current, 'API Access')?.documentNavigation).not.toBe(
      true
    )
    expect(findLink(result.current, 'Docs')?.href).toBe(
      'https://docs.example.com/guide'
    )
  })

  test('the marketing home entry is unchanged', () => {
    const { result } = renderNavLinks({})

    const home = findLink(result.current, 'Home')
    expect(home?.href).toBe('https://chuanapi.com/')
    expect(home?.documentNavigation).toBe(true)
  })

  test('docs:false in HeaderNavModules hides the docs entry', () => {
    const { result } = renderNavLinks({
      HeaderNavModules: { docs: false },
    })

    expect(findLink(result.current, 'Docs')).toBeUndefined()
    expect(findLink(result.current, 'API Access')).toBeUndefined()
    expect(findLink(result.current, 'Home')?.href).toBe('https://chuanapi.com/')
  })
})
