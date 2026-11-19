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
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { act, render, screen, within, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import InvoicePage from '..'

afterEach(() => {
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})
function renderCenter() {
  useAuthStore.getState().auth.setUser({ id: 1, role: 1, username: 'example' })
  const routeTree = createRootRoute({ component: InvoicePage })
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
}
const totals = {
  available_minor: '123',
  available_count: 2,
  pending_minor: '1',
  pending_count: 1,
  issued_minor: '0',
  issued_count: 0,
  currency: 'CNY',
}
const entry = {
  id: 4,
  status: 'pending',
  amount_minor: '1',
  currency: 'CNY',
  order_count: 1,
  invoice_type: 'ordinary',
  title_type: 'personal',
  category: 'technical_service',
  created_at: 1,
  updated_at: 1,
  has_document: false,
}
test('select all uses only the displayed page; switching accounts drops all selected orders', async () => {
  const user = userEvent.setup()
  vi.spyOn(api, 'get').mockImplementation(async (path, config) => {
    let data: unknown
    if (path === '/api/invoice/config') data = { enabled: true }
    if (path === '/api/invoice/summary') data = totals
    if (path === '/api/invoice/applications') data = { items: [], total: 0 }
    if (path === '/api/invoice/orders') {
      const second = (config?.params as { p?: number } | undefined)?.p === 2
      const owner = useAuthStore.getState().auth.user?.id
      data = {
        items: [
          {
            id: second ? 21 : 1,
            reference: `EXAMPLE-${owner}-${second ? 21 : 1}`,
            paid_at: 123,
            amount_minor: second ? '22' : '101',
            type: 'topup',
            currency: 'CNY',
          },
        ],
        total: 21,
      }
    }
    return { data: { success: true, data } }
  })
  renderCenter()
  const submit = await screen.findByRole('button', {
    name: 'Submit application',
  })
  expect(submit).toBeDisabled()
  await user.click(
    await screen.findByRole('checkbox', { name: 'Select this page' })
  )
  expect(submit).toBeEnabled()
  await user.click(screen.getByRole('button', { name: 'Next page' }))
  const second = await screen.findByRole('checkbox', { name: 'EXAMPLE-1-21' })
  expect(second).not.toBeChecked()
  await user.click(second)
  expect(screen.getByText('Selected orders').parentElement).toHaveTextContent(
    '2 orders'
  )
  expect(screen.getByText('Invoice amount').parentElement).toHaveTextContent(
    '1.23'
  )
  act(() =>
    useAuthStore
      .getState()
      .auth.setUser({ id: 2, role: 1, username: 'example2' })
  )
  expect(
    await screen.findByRole('checkbox', { name: 'EXAMPLE-2-1' })
  ).not.toBeChecked()
  expect(
    screen.getByRole('button', { name: 'Submit application' })
  ).toBeDisabled()
})
test('closed submissions retain access to details and confirmed cancellation of an existing pending application', async () => {
  const user = userEvent.setup()
  let cancelled = false
  const get = vi.spyOn(api, 'get').mockImplementation(async (path) => {
    let data: unknown
    if (path === '/api/invoice/config') data = { enabled: false }
    if (path === '/api/invoice/summary') data = totals
    if (path === '/api/invoice/applications') {
      data = {
        items: [{ ...entry, status: cancelled ? 'cancelled' : 'pending' }],
        total: 1,
      }
    }
    if (path === '/api/invoice/applications/4') {
      data = {
        ...entry,
        title: 'Test title',
        email: 'test@example.invalid',
        orders: [],
      }
    }
    return { data: { success: true, data } }
  })
  const post = vi.spyOn(api, 'post').mockImplementation(async () => {
    cancelled = true
    return { data: { success: true, data: { id: 4 } } }
  })
  renderCenter()
  await screen.findByText(/New applications are currently closed/)
  expect(
    screen.queryByRole('button', { name: 'Submit application' })
  ).toBeNull()
  expect(get.mock.calls.some(([path]) => path === '/api/invoice/orders')).toBe(
    false
  )
  await user.click(await screen.findByRole('button', { name: 'Details #4' }))
  const detail = await screen.findByRole('dialog')
  expect(await within(detail).findByText('Test title')).toBeVisible()
  expect(
    within(detail).queryByRole('button', { name: 'Download PDF' })
  ).toBeNull()
  await user.click(
    within(detail).getByRole('button', { name: 'Cancel application' })
  )
  let confirm = await screen.findByRole('alertdialog')
  await user.click(
    within(confirm).getByRole('button', { name: 'Keep application' })
  )
  expect(post).not.toHaveBeenCalled()
  await user.click(
    within(detail).getByRole('button', { name: 'Cancel application' })
  )
  confirm = await screen.findByRole('alertdialog')
  await user.click(
    within(confirm).getByRole('button', { name: 'Cancel application' })
  )
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith('/api/invoice/applications/4/cancel')
  )
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  expect(
    await screen.findByText('Cancelled', { selector: 'span' })
  ).toBeVisible()
})

test('refresh preserves the typed draft while clearing selections whose eligibility may have changed', async () => {
  const user = userEvent.setup()
  let ordersVisible = true
  vi.spyOn(api, 'get').mockImplementation(async (path) => {
    let data: unknown = { items: [], total: 0 }
    if (path === '/api/invoice/config') data = { enabled: true }
    if (path === '/api/invoice/summary') data = totals
    if (path === '/api/invoice/orders' && ordersVisible) {
      data = {
        items: [
          {
            id: 2,
            reference: 'EXAMPLE-2',
            amount_minor: '5',
            paid_at: 1,
            type: 'topup',
            currency: 'CNY',
          },
        ],
        total: 1,
      }
    }
    return { data: { success: true, data } }
  })
  renderCenter()
  await user.click(await screen.findByRole('checkbox', { name: 'EXAMPLE-2' }))
  const title = screen.getByRole('textbox', { name: 'Invoice title' })
  await user.type(title, 'Draft title')
  ordersVisible = false
  await user.click(await screen.findByRole('button', { name: 'Refresh' }))
  expect(await screen.findByText('No eligible orders')).toBeInTheDocument()
  expect(
    screen.getByRole('button', { name: 'Submit application' })
  ).toBeDisabled()
  expect(title).toHaveValue('Draft title')
  expect(
    screen.getByRole('link', { name: 'Contact support for manual review.' })
  ).toHaveAttribute('href', '/support')
})

test('a finance claim racing with cancellation refreshes the pending status without offering cancellation or a PDF', async () => {
  const user = userEvent.setup()
  let processing = false
  vi.spyOn(api, 'get').mockImplementation(async (path) => {
    let data: unknown
    if (path === '/api/invoice/config') data = { enabled: false }
    if (path === '/api/invoice/summary') data = totals
    const pending = { ...entry, processing }
    if (path === '/api/invoice/applications') {
      data = { items: [pending], total: 1 }
    }
    if (path === '/api/invoice/applications/4') {
      data = {
        ...pending,
        title: 'Test title',
        email: 'test@example.invalid',
        orders: [],
      }
    }
    return { data: { success: true, data } }
  })
  const post = vi.spyOn(api, 'post').mockImplementation(async () => {
    processing = true
    throw Object.assign(new Error('Conflict'), {
      response: { status: 409, data: { success: false, message: 'Conflict' } },
    })
  })
  renderCenter()
  await user.click(await screen.findByRole('button', { name: 'Details #4' }))
  const dialog = await screen.findByRole('dialog')
  await user.click(
    await within(dialog).findByRole('button', { name: 'Cancel application' })
  )
  await user.click(
    within(await screen.findByRole('alertdialog')).getByRole('button', {
      name: 'Cancel application',
    })
  )
  expect(await within(dialog).findByText('Invoice processing')).toBeVisible()
  expect(
    within(dialog).queryByRole('button', { name: 'Cancel application' })
  ).not.toBeInTheDocument()
  expect(
    within(dialog).queryByRole('button', { name: 'Download PDF' })
  ).not.toBeInTheDocument()
  expect(
    await screen.findByText('Invoice processing', { selector: 'span' })
  ).toBeInTheDocument()
  expect(post).toHaveBeenCalledOnce()
})
