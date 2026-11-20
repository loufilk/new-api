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
import { afterEach, expect, test } from 'vitest'

import { ROLE } from '@/lib/roles'
import { STATUS_QUERY_KEY } from '@/lib/status-query'
import { useAuthStore } from '@/stores/auth-store'

import { useSidebarData } from '../use-sidebar-data'

afterEach(() => useAuthStore.getState().auth.reset())

test.each([
  {
    name: 'shows the center when new visibility is on but applications are closed',
    signedIn: true,
    status: {
      customer_invoice_center_visible: true,
      customer_invoice_enabled: false,
    },
    visible: true,
  },
  {
    name: 'hides the center when new visibility is off even if the legacy field was on',
    signedIn: true,
    status: {
      customer_invoice_center_visible: false,
      customer_invoice_enabled: true,
    },
    visible: false,
  },
  {
    name: 'uses the legacy enabled field when visibility is omitted',
    signedIn: true,
    status: { customer_invoice_enabled: true },
    visible: true,
  },
  {
    name: 'hides the center when the legacy server says it is disabled',
    signedIn: true,
    status: { customer_invoice_enabled: false },
    visible: false,
  },
  {
    name: 'hides the center when neither status field is known',
    signedIn: true,
    status: {},
    visible: false,
  },
  {
    name: 'hides the center when new visibility is null rather than omitted',
    signedIn: true,
    status: {
      customer_invoice_center_visible: null,
      customer_invoice_enabled: true,
    },
    visible: false,
  },
  {
    name: 'hides the center when new visibility is a malformed string',
    signedIn: true,
    status: {
      customer_invoice_center_visible: 'true',
      customer_invoice_enabled: true,
    },
    visible: false,
  },
  {
    name: 'hides the center from signed-out users',
    signedIn: false,
    status: {
      customer_invoice_center_visible: true,
      customer_invoice_enabled: true,
    },
    visible: false,
  },
])('$name', ({ signedIn, status, visible }) => {
  if (signedIn) {
    useAuthStore
      .getState()
      .auth.setUser({ id: 7, username: 'customer', role: ROLE.USER })
  }
  const client = new QueryClient()
  client.setQueryData(STATUS_QUERY_KEY, status)
  const { result, unmount } = renderHook(() => useSidebarData(), {
    wrapper: (props) => (
      <QueryClientProvider client={client}>
        {props.children}
      </QueryClientProvider>
    ),
  })
  const personal = result.current.navGroups.find(
    (group) => group.id === 'personal'
  )
  const invoice = personal?.items.some(
    (item) => 'url' in item && item.url === '/invoice'
  )
  unmount()
  client.clear()

  expect(invoice).toBe(visible)
})
