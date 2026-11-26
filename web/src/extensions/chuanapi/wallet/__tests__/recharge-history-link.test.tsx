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
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from '@tanstack/react-router'
import {
  act,
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import '@/extensions/bootstrap'
import { api } from '@/lib/api'
import { Route as WalletRoute } from '@/routes/_authenticated/wallet/index'
import { useAuthStore } from '@/stores/auth-store'

import { Wallet } from '../index'

const historyName = { name: 'Recharge history' }
const currentUser = { id: 8, username: 'wallet-owner', role: 1 }

beforeEach(() => {
  useAuthStore.getState().auth.setUser(currentUser)
  vi.spyOn(window, 'scrollTo').mockImplementation(() => undefined)
  vi.spyOn(api, 'get').mockImplementation(async (url: string) => {
    let data: unknown = { items: [], total: 0, methods: [] }
    if (url === '/api/user/self') data = currentUser
    if (url === '/api/status') data = {}
    if (url === '/api/user/wallet') {
      data = { paid: 0, bonus: 0, reward: 0, total: 0, rules: {} }
    }
    if (url === '/api/user/aff') data = 'self'
    if (url === '/api/user/topup/info') {
      data = {
        enable_online_topup: true,
        min_topup: 1,
        pay_methods: [{ type: 'epay', name: 'Epay' }],
      }
    }
    return { data: { success: true, data } }
  })
  vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: '1' },
  })
})

afterEach(() => {
  cleanup()
  useAuthStore.getState().auth.reset()
})

function renderWallet(entry: string) {
  const root = createRootRoute({ component: Outlet })
  const auth = createRoute({
    getParentRoute: () => root,
    id: '_authenticated',
    component: Outlet,
  })
  const wallet = createRoute({
    getParentRoute: () => auth,
    path: '/wallet/',
    component: WalletRoute.options.component,
    validateSearch: WalletRoute.options.validateSearch,
  })
  const router = createRouter({
    routeTree: root.addChildren([auth.addChildren([wallet])]),
    history: createMemoryHistory({ initialEntries: [entry] }),
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  return router
}

test('the payment return URL opens only personal recharge history and dismisses to a reload-safe wallet URL', async () => {
  const user = userEvent.setup()
  const router = renderWallet('/wallet?show_history=true')
  const dialog = await screen.findByRole('dialog', historyName)
  expect(
    await within(dialog).findByText('No recharge records yet')
  ).toBeVisible()
  expect(api.get).toHaveBeenCalledWith(
    '/api/user/topup/history?p=1&page_size=10',
    expect.objectContaining({ signal: expect.anything() })
  )
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  // Wallet loading may quote the default amount, but never creates an order.
  expect(
    vi.mocked(api.post).mock.calls.every(([url]) => url === '/api/user/amount')
  ).toBe(true)

  await user.click(within(dialog).getByRole('button', { name: 'Close' }))
  await waitFor(() =>
    expect(screen.queryByRole('dialog', historyName)).not.toBeInTheDocument()
  )
  expect(router.state.location.searchStr).toBe('')

  await user.click(screen.getByRole('button', historyName))
  expect(await screen.findByRole('dialog', historyName)).toBeVisible()
  await user.keyboard('{Escape}')
  await waitFor(() =>
    expect(screen.queryByRole('dialog', historyName)).not.toBeInTheDocument()
  )
})

test('navigating to a return link in an already mounted wallet opens history and Back closes it', async () => {
  const router = renderWallet('/wallet?show_history=false')
  await screen.findByRole('button', historyName)
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(api.get).not.toHaveBeenCalledWith(
    '/api/user/topup/history?p=1&page_size=10',
    expect.objectContaining({ signal: expect.anything() })
  )

  await act(async () => {
    await router.navigate({ to: '/wallet', search: { show_history: true } })
  })
  expect(await screen.findByRole('dialog', historyName)).toBeVisible()
  act(() => router.history.back())
  await waitFor(() =>
    expect(screen.queryByRole('dialog', historyName)).not.toBeInTheDocument()
  )
  act(() => router.history.forward())
  expect(await screen.findByRole('dialog', historyName)).toBeVisible()
})

test('a standalone wallet can dismiss an initially open history dialog without a navigation callback', async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  render(
    <QueryClientProvider client={client}>
      <Wallet initialShowHistory />
    </QueryClientProvider>
  )
  const dialog = await screen.findByRole('dialog', historyName)
  await userEvent
    .setup()
    .click(within(dialog).getByRole('button', { name: 'Close' }))
  await waitFor(() =>
    expect(screen.queryByRole('dialog', historyName)).not.toBeInTheDocument()
  )
})

test('history searches full order numbers with server-side methods and resets filters on account change', async () => {
  const user = userEvent.setup()
  const get = vi.mocked(api.get)
  const defaultGet = get.getMockImplementation()
  const older = 'USDT-older-than-50'
  get.mockImplementation(async (url, ...args) => {
    if (!url.startsWith('/api/user/topup/history?')) {
      if (!defaultGet) throw new Error('Missing wallet API fixture')
      return defaultGet(url, ...args)
    }
    const params = new URLSearchParams(url.split('?')[1])
    const owner = useAuthStore.getState().auth.user?.id === currentUser.id
    const method = params.get('method')
    const order = params.get('order_no')
    const items =
      owner && method === 'usdt' && order === older
        ? [
            {
              order_no: older,
              created_at: 1735689600,
              usdt: {
                network: 'tron',
                status: 'paid',
                quote_usdt: '14.234501',
                wallet_credit_cny: '101.25',
              },
            },
          ]
        : []
    return {
      data: {
        success: true,
        data: {
          methods: owner ? ['fiat:alipay', 'usdt'] : [],
          items,
          total: items.length || (!order && owner ? 55 : 0),
        },
      },
    }
  })

  renderWallet('/wallet?show_history=true')
  const dialog = await screen.findByRole('dialog', historyName)
  const history = within(dialog)
  await history.findByRole('option', { name: 'USDT' })
  await user.click(await history.findByLabelText('Go to next page'))
  await waitFor(() =>
    expect(get).toHaveBeenCalledWith(
      '/api/user/topup/history?p=2&page_size=10',
      expect.anything()
    )
  )
  await user.selectOptions(
    history.getByRole('combobox', { name: 'Payment method' }),
    'usdt'
  )
  await user.type(history.getByRole('searchbox', { name: 'Order ID' }), older)
  await user.click(history.getByRole('button', { name: 'Search' }))
  const row = await history.findByRole('row', { name: /USDT-older-than-50/ })
  expect(row).toHaveTextContent('14.234501 USDT')
  expect(row).toHaveTextContent('USDT order quote')
  expect(row).toHaveTextContent('CN¥101.25')
  expect(row).toHaveTextContent('USDT · TRC20')
  expect(get).toHaveBeenCalledWith(
    '/api/user/topup/history?p=1&page_size=10&order_no=USDT-older-than-50&method=usdt',
    expect.anything()
  )

  act(() =>
    useAuthStore
      .getState()
      .auth.setUser({ id: 9, username: 'another', role: 1 })
  )
  expect(history.queryByText(older)).not.toBeInTheDocument()
  expect(history.getByRole('searchbox', { name: 'Order ID' })).toHaveValue('')
  expect(history.getByRole('combobox', { name: 'Payment method' })).toHaveValue(
    ''
  )
  expect(await history.findByText('No recharge records yet')).toBeVisible()
})
