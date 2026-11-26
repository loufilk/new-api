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
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { withinCnyLimits, type UsdtOrder } from '../../usdt-api'
import { UsdtRecharge } from '../usdt-recharge'

const calls = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('@/lib/api', () => ({ api: calls }))

const order: UsdtOrder = {
  id: 'isolated-order',
  network: 'polygon',
  amount: '60',
  usdt_amount: '8.099007000000000005',
  wallet_credit: '60',
  bonus_credit: '1.50',
  address: '0x0000000000000000000000000000000000000001',
  status: 'pending',
  expires_at: '2099-01-02T11:10:00Z',
}
const ok = (data: unknown) => ({ data: { success: true, data } })
function setServer(
  enabled = true,
  history: UsdtOrder[] = [],
  detail = order,
  optionsUnavailable = false,
  networks = [{ id: 'polygon', name: 'Polygon', bonus_bps: 250 }]
) {
  calls.get.mockImplementation(async (url: string) => {
    if (url.endsWith('/options')) {
      if (optionsUnavailable) throw new Error('Options temporarily unavailable')
      return ok({
        enabled,
        currency: 'CNY',
        networks: enabled ? networks : [],
        min_amount: '10',
        max_amount: '200',
      })
    }
    if (url.includes('/order-requests/')) {
      return ok({ idempotency_key: url.split('/').at(-1), order: detail })
    }
    if (url.endsWith('/orders')) return ok(history)
    if (url.includes('/orders/')) return ok(detail)
    throw new Error('Unexpected URL')
  })
}
function mount(userId = 7) {
  return {
    ...render(
      <QueryClientProvider client={new QueryClient()}>
        <UsdtRecharge userId={userId} onPaid={() => undefined} />
      </QueryClientProvider>
    ),
    user: userEvent.setup(),
  }
}
async function prepare(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByRole('button', { name: 'Pay with USDT' }))
  const network = screen.getByRole('combobox', { name: 'Payment network' })
  expect(
    within(network)
      .getAllByRole('option')
      .map((item) => item.textContent)
  ).toEqual(['Select a network', 'Polygon'])
  await user.selectOptions(network, 'polygon')
  await user.type(
    screen.getByRole('textbox', { name: 'Recharge principal (CNY)' }),
    '60'
  )
  await user.click(screen.getByRole('button', { name: 'Review USDT order' }))
}

beforeEach(() => {
  vi.resetAllMocks()
  sessionStorage.clear()
  setServer()
})

describe('USDT wallet rail', () => {
  test('requires confirmation and reveals only a direct QR after acknowledging the quote', async () => {
    let settle: (() => void) | undefined
    calls.post.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          settle = () =>
            resolve(
              ok({ ...order, checkout_url: 'https://cashier.test/order' })
            )
        })
    )
    const { user } = mount()
    await prepare(user)
    expect(calls.post).not.toHaveBeenCalled()
    const submit = screen.getByRole('button', {
      name: 'Confirm and create order',
    })
    fireEvent.click(submit)
    fireEvent.click(submit)
    expect(calls.post).toHaveBeenCalledTimes(1)
    settle?.()
    expect(await screen.findByText('8.099007000000000005 USDT')).toBeVisible()
    expect(screen.getByText('CNY 60', { exact: true })).toBeVisible()
    expect(screen.getByText('60 wallet units')).toBeVisible()
    expect(screen.getByText('1.50 wallet units')).toBeVisible()
    expect(
      screen.queryByRole('link', { name: /Open USDT checkout/ })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('img', { name: 'USDT receiving address QR code' })
    ).not.toBeInTheDocument()
    await user.click(
      await screen.findByRole('checkbox', { name: /I checked the network/ })
    )
    expect(
      screen.getByRole('img', { name: 'USDT receiving address QR code' })
    ).toBeVisible()
    expect(screen.getByText(/Do not switch networks or renew/)).toBeVisible()
    expect(screen.queryByRole('link')).not.toBeInTheDocument()
  })

  test('a request-bound pre-insert refusal refreshes options and unlocks the form', async () => {
    calls.post.mockImplementationOnce(async (_url, draft) => {
      setServer(false)
      return {
        status: 400,
        data: {
          success: false,
          code: 'usdt_terms_rejected_before_insert',
          ...draft,
        },
      }
    })
    const { user } = mount()
    await prepare(user)
    await user.click(
      screen.getByRole('button', { name: 'Confirm and create order' })
    )
    expect(await screen.findByText(/Recharge options changed/)).toBeVisible()
    expect(
      screen.queryByRole('button', { name: 'Check order status' })
    ).not.toBeInTheDocument()
    expect(sessionStorage.getItem('wallet-usdt-unconfirmed:7')).toBeNull()
    expect(
      calls.get.mock.calls.filter(([url]) => url.endsWith('/options')).length
    ).toBeGreaterThan(1)
    expect(
      screen.queryByRole('button', { name: 'Confirm and create order' })
    ).not.toBeInTheDocument()
    expect(calls.post).toHaveBeenCalledTimes(1)
  })

  test('changed terms with another available network allow a fresh, explicit review', async () => {
    calls.post.mockImplementationOnce(async (_url, draft) => {
      setServer(true, [], order, false, [
        { id: 'bsc', name: 'BSC', bonus_bps: 0 },
      ])
      return {
        status: 400,
        data: {
          success: false,
          code: 'usdt_terms_rejected_before_insert',
          ...draft,
        },
      }
    })
    const { user } = mount()
    await prepare(user)
    await user.click(
      screen.getByRole('button', { name: 'Confirm and create order' })
    )
    expect(await screen.findByText(/Recharge options changed/)).toBeVisible()
    const selector = screen.getByRole('combobox', { name: 'Payment network' })
    expect(selector).toBeEnabled()
    expect(selector).toHaveValue('')
    expect(
      within(selector).queryByRole('option', { name: 'Polygon' })
    ).toBeNull()
    expect(
      within(selector).getByRole('option', { name: 'BSC (BEP-20)' })
    ).toBeVisible()
    expect(
      screen.getByRole('textbox', { name: 'Recharge principal (CNY)' })
    ).toBeEnabled()
    expect(
      screen.getByRole('button', { name: 'Review USDT order' })
    ).toBeDisabled()
    expect(calls.post).toHaveBeenCalledTimes(1)
  })

  test.each(['proxy-400', 'wrong-key', 'outside-400', 'duplicate', 'timeout'])(
    '%s keeps the original receipt; recovery never re-posts even when lookup is inconclusive',
    async (kind) => {
      calls.post.mockImplementationOnce(async (_url, draft) => {
        if (kind === 'timeout' || kind === 'duplicate') throw new Error(kind)
        return {
          status: kind === 'outside-400' ? 200 : 400,
          data:
            kind === 'proxy-400'
              ? '<html>Bad request</html>'
              : {
                  success: false,
                  code: 'usdt_terms_rejected_before_insert',
                  ...draft,
                  idempotency_key:
                    kind === 'wrong-key'
                      ? 'different-receipt'
                      : draft.idempotency_key,
                },
        }
      })
      const { user } = mount()
      await prepare(user)
      await user.click(
        screen.getByRole('button', { name: 'Confirm and create order' })
      )
      const button = await screen.findByRole('button', {
        name: 'Check order status',
      })
      await waitFor(() => expect(button).toBeEnabled())
      calls.get.mockRejectedValueOnce(new Error('not visible yet'))
      await user.click(button)
      await waitFor(() => expect(button).toBeEnabled())
      expect(sessionStorage.getItem('wallet-usdt-unconfirmed:7')).toContain(
        calls.post.mock.calls[0][1].idempotency_key
      )
      expect(screen.getByText(/Order not confirmed yet/)).toBeVisible()
      expect(
        screen.queryByRole('button', { name: 'Review USDT order' })
      ).not.toBeInTheDocument()
      expect(calls.post).toHaveBeenCalledTimes(1)
    }
  )

  test('reload after a lost response looks up the owner receipt, even with no eligible network and empty recent history', async () => {
    calls.post.mockRejectedValueOnce(new Error('ambiguous'))
    const first = mount()
    await prepare(first.user)
    await first.user.click(
      screen.getByRole('button', { name: 'Confirm and create order' })
    )
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Check order status' })
      ).toBeEnabled()
    )
    first.unmount()
    const other = mount(9)
    await other.user.click(
      await screen.findByRole('button', { name: 'Pay with USDT' })
    )
    expect(
      screen.queryByRole('button', { name: 'Check order status' })
    ).not.toBeInTheDocument()
    other.unmount()
    setServer(false)
    const restored = mount()
    await restored.user.click(
      await screen.findByRole('button', { name: 'USDT orders' })
    )
    expect(calls.post).toHaveBeenCalledTimes(1)
    await restored.user.click(
      await screen.findByRole('button', { name: 'Check order status' })
    )
    expect(await screen.findByText('8.099007000000000005 USDT')).toBeVisible()
    expect(
      calls.get.mock.calls.some(
        ([path]) =>
          path ===
          `/api/user/usdt/order-requests/${calls.post.mock.calls[0][1].idempotency_key}`
      )
    ).toBe(true)
    expect(calls.post).toHaveBeenCalledTimes(1)
    expect(sessionStorage.getItem('wallet-usdt-unconfirmed:7')).toBeNull()
    expect(screen.queryByRole('img')).not.toBeInTheDocument()
  })

  test('when disabled only previously saved orders may be viewed', async () => {
    setServer(false, [order])
    const { user } = mount()
    await user.click(await screen.findByRole('button', { name: 'USDT orders' }))
    expect(await screen.findByText('8.099007000000000005 USDT')).toBeVisible()
    expect(
      screen.queryByRole('button', { name: 'Create a different order' })
    ).not.toBeInTheDocument()
    expect(calls.post).not.toHaveBeenCalled()
  })

  test('a prior order remains recheckable after its specific network is disabled', async () => {
    const prior = { ...order, network: 'bsc' as const }
    setServer(true, [prior], prior)
    const { user } = mount()
    await user.click(
      await screen.findByRole('button', { name: 'Pay with USDT' })
    )
    expect(await screen.findByText('8.099007000000000005 USDT')).toBeVisible()
    await user.click(
      await screen.findByRole('checkbox', { name: /I checked the network/ })
    )
    expect(
      screen.getByRole('img', { name: 'USDT receiving address QR code' })
    ).toBeVisible()
    expect(calls.post).not.toHaveBeenCalled()
  })

  test('configuration failure leaves existing history accessible and new orders disabled', async () => {
    setServer(true, [order], order, true)
    const { user } = mount()
    await user.click(await screen.findByRole('button', { name: 'USDT orders' }))
    expect(await screen.findByText('8.099007000000000005 USDT')).toBeVisible()
    expect(
      screen.queryByRole('button', { name: 'Create a different order' })
    ).not.toBeInTheDocument()
    expect(calls.post).not.toHaveBeenCalled()
  })

  test('when disabled with no orders the rail stays hidden', async () => {
    setServer(false)
    mount()
    await waitFor(() =>
      expect(calls.get).toHaveBeenCalledWith(
        '/api/user/usdt/orders',
        expect.anything()
      )
    )
    expect(
      screen.queryByRole('region', { name: 'USDT wallet recharge' })
    ).not.toBeInTheDocument()
  })

  test.each(['expired', 'review'] as const)(
    '%s has a warning and no active payment controls',
    async (status) => {
      const detail = { ...order, status }
      setServer(true, [detail], detail)
      const { user } = mount()
      await user.click(
        await screen.findByRole('button', { name: 'Pay with USDT' })
      )
      await user.click(
        await screen.findByRole('button', { name: /Polygon · CNY 60/ })
      )
      expect(await screen.findByRole('alert')).toHaveTextContent(
        status === 'expired' ? 'Do not send funds' : 'Do not transfer again'
      )
      expect(screen.queryByRole('checkbox')).not.toBeInTheDocument()
    }
  )

  test('validates exact two-decimal principal bounds', () => {
    expect(withinCnyLimits('10.00', '10', '25.50')).toBe(true)
    expect(withinCnyLimits('25.51', '10', '25.50')).toBe(false)
    expect(withinCnyLimits('10.001', '10', '25.50')).toBe(false)
  })
})
