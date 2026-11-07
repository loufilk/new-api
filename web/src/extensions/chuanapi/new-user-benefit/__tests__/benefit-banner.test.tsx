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
import {
  QueryClient,
  QueryClientProvider,
  focusManager,
} from '@tanstack/react-query'
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { toast, Toaster } from 'sonner'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { createAppQueryClient } from '@/lib/query-client'
import { useAuthStore } from '@/stores/auth-store'

import { loadChuanapiI18nOverride } from '../../i18n'
import { useWalletSummary } from '../../wallet/hooks/use-wallet'
import { NewUserBenefitBanner } from '../benefit-banner'
import { BENEFIT_QUERY_KEY } from '../lib'
import type { BenefitCheckout, NewUserBenefitInfo } from '../types'

let info: NewUserBenefitInfo
let clients: QueryClient[] = []
let submitted: Array<{ action: string; money: string | undefined }>

function checkout(pay = '0.10', credit = '1.00'): BenefitCheckout {
  return {
    order_id: 'benefit-order-1',
    pay_amount_cny: pay,
    credit_amount_cny: credit,
    status: 'pending',
    checkout: {
      kind: 'form',
      url: 'https://payments.example/submit.php',
      params: { money: pay, out_trade_no: 'benefit-order-1' },
    },
  }
}

beforeEach(() => {
  info = {
    enabled: true,
    eligible: true,
    status: 'available',
    pay_amount_cny: '0.10',
    credit_amount_cny: '1.00',
    paid_quota: 7143,
    bonus_quota: 64286,
    config_version: 1,
    pay_methods: [{ type: 'alipay', name: 'Alipay' }],
    turnstile_required: false,
    turnstile_site_key: '',
  }
  submitted = []
  useAuthStore.getState().auth.setUser({ id: 7, username: 'newcomer', role: 1 })
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    expect(url).toBe('/api/user/new-user-benefit')
    return { data: { success: true, data: structuredClone(info) } }
  })
  vi.spyOn(HTMLFormElement.prototype, 'submit').mockImplementation(function (
    this: HTMLFormElement
  ) {
    submitted.push({
      action: this.action,
      money: this.querySelector<HTMLInputElement>('input[name="money"]')?.value,
    })
  })
})

afterEach(() => {
  cleanup()
  clients.forEach((client) => client.clear())
  clients = []
  useAuthStore.getState().auth.reset()
  focusManager.setFocused(undefined)
})

function renderBanner(persistent = false) {
  const client = createAppQueryClient()
  client.setDefaultOptions({
    queries: {
      ...client.getDefaultOptions().queries,
      retry: false,
      gcTime: Infinity,
    },
    mutations: { retry: false },
  })
  clients.push(client)
  const content = (keepVisible: boolean) => (
    <QueryClientProvider client={client}>
      <NewUserBenefitBanner persistent={keepVisible} />
      <Toaster />
    </QueryClientProvider>
  )
  const view = render(content(persistent))
  return {
    ...view,
    client,
    user: userEvent.setup(),
    persistent: () => view.rerender(content(true)),
  }
}

async function openDialog() {
  fireEvent.click(
    await screen.findByRole('button', { name: 'View recharge benefit' })
  )
  return screen.findByRole('alertdialog')
}

test('the banner and confirmation show CNY 0.10 for total CNY 1.00 and cancel never pays', async () => {
  const post = vi.spyOn(api, 'post')
  const { user } = renderBanner()
  const banner = await screen.findByRole('region', {
    name: 'New user recharge benefit',
  })
  expect(banner).toHaveTextContent(
    'Pay CN¥0.10 and get CN¥1.00 in total credit'
  )
  expect(banner.textContent).not.toMatch(/free/i)
  const dialog = await openDialog()
  expect(dialog).toHaveTextContent(
    'Pay CN¥0.10 to receive CN¥1.00 in total credit after payment.'
  )
  expect(dialog).toHaveTextContent('bonus balance')
  expect(post).not.toHaveBeenCalled()
  await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
  expect(post).not.toHaveBeenCalled()
  expect(submitted).toEqual([])
})

test('two fast confirmations create one checkout with a UUID and preserve fractional payment', async () => {
  let complete: ((value: unknown) => void) | undefined
  const post = vi.spyOn(api, 'post').mockImplementation(
    () =>
      new Promise((resolve) => {
        complete = resolve
      })
  )
  renderBanner()
  const dialog = await openDialog()
  const pay = within(dialog).getByRole('button', {
    name: 'Pay CN¥0.10 and continue',
  })
  act(() => {
    fireEvent.click(pay)
    fireEvent.click(pay)
  })
  await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
  expect(pay).toBeDisabled()
  expect(post.mock.calls[0][0]).toBe('/api/user/new-user-benefit/pay')
  expect(post.mock.calls[0][1]).toEqual({
    payment_method: 'alipay',
    config_version: 1,
  })
  const key = post.mock.calls[0][2]?.headers?.['Idempotency-Key']
  expect(key).toMatch(
    /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i
  )
  await act(async () =>
    complete?.({ data: { success: true, data: checkout() } })
  )
  await waitFor(() =>
    expect(submitted).toEqual([
      { action: 'https://payments.example/submit.php', money: '0.10' },
    ])
  )
})

test('a network failure keeps the confirmation open and retries with the same idempotency key', async () => {
  const post = vi
    .spyOn(api, 'post')
    .mockRejectedValueOnce(new Error('Payment network unavailable'))
    .mockResolvedValueOnce({ data: { success: true, data: checkout() } })
  const { user } = renderBanner()
  const dialog = await openDialog()
  const pay = within(dialog).getByRole('button', {
    name: 'Pay CN¥0.10 and continue',
  })
  await user.click(pay)
  await waitFor(() => expect(pay).toBeEnabled())
  expect(screen.getByRole('alertdialog')).toBeVisible()
  expect(submitted).toEqual([])
  await user.click(pay)
  await waitFor(() => expect(submitted).toHaveLength(1))
  expect(post).toHaveBeenCalledTimes(2)
  expect(post.mock.calls[1][2]?.headers?.['Idempotency-Key']).toBe(
    post.mock.calls[0][2]?.headers?.['Idempotency-Key']
  )
})

test('a configuration conflict refreshes the amount and requires another explicit confirmation', async () => {
  const errorToast = vi.spyOn(toast, 'error')
  const post = vi
    .spyOn(api, 'post')
    .mockImplementationOnce(async () => {
      info.pay_amount_cny = '0.25'
      info.credit_amount_cny = '2.00'
      info.config_version = 2
      throw Object.assign(new Error('Conflict'), {
        response: {
          status: 409,
          data: { code: 'new_user_benefit_config_changed' },
        },
      })
    })
    .mockResolvedValueOnce({
      data: { success: true, data: checkout('0.25', '2.00') },
    })
  const { user } = renderBanner()
  const dialog = await openDialog()
  await user.click(
    within(dialog).getByRole('button', { name: 'Pay CN¥0.10 and continue' })
  )
  expect(
    await screen.findByText(
      'The offer changed. Review the updated amounts and confirm again.'
    )
  ).toBeVisible()
  const updated = screen.getByRole('button', {
    name: 'Pay CN¥0.25 and continue',
  })
  expect(screen.getByRole('alertdialog')).toHaveTextContent('CN¥2.00')
  expect(post).toHaveBeenCalledTimes(1)
  expect(submitted).toEqual([])
  expect(errorToast).not.toHaveBeenCalled()
  await user.click(updated)
  await waitFor(() => expect(post).toHaveBeenCalledTimes(2))
  expect(post.mock.calls[1][1]).toEqual({
    payment_method: 'alipay',
    config_version: 2,
  })
  expect(post.mock.calls[1][2]?.headers?.['Idempotency-Key']).not.toBe(
    post.mock.calls[0][2]?.headers?.['Idempotency-Key']
  )
})

test('disabling the offer after confirmation gives clear feedback without opening payment', async () => {
  const errorToast = vi.spyOn(toast, 'error')
  const post = vi.spyOn(api, 'post').mockImplementationOnce(async () => {
    info.enabled = false
    info.eligible = false
    throw Object.assign(new Error('Conflict'), {
      response: {
        status: 409,
        data: { code: 'new_user_benefit_config_changed' },
      },
    })
  })
  const { user } = renderBanner()
  const dialog = await openDialog()
  await user.click(
    within(dialog).getByRole('button', { name: 'Pay CN¥0.10 and continue' })
  )
  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
  expect(post).toHaveBeenCalledTimes(1)
  expect(errorToast).toHaveBeenCalledTimes(1)
  expect(errorToast).toHaveBeenCalledWith(
    'New user benefit is temporarily unavailable.'
  )
  expect(submitted).toEqual([])
})

test('pending orders retain their original method, amount and version after disabling and repricing', async () => {
  info = {
    ...info,
    enabled: false,
    eligible: false,
    status: 'pending',
    config_version: 9,
    pay_amount_cny: '8.00',
    credit_amount_cny: '10.00',
    pay_methods: [
      { type: 'wxpay', name: 'WeChat' },
      { type: 'alipay', name: 'Alipay' },
    ],
    pending_order: {
      order_id: 'benefit-order-1',
      pay_amount_cny: '0.10',
      credit_amount_cny: '1.00',
      payment_method: 'alipay',
      config_version: 1,
    },
  }
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue({ data: { success: true, data: checkout() } })
  const { user } = renderBanner()
  await user.click(
    await screen.findByRole('button', { name: 'Continue benefit payment' })
  )
  const dialog = screen.getByRole('alertdialog')
  expect(dialog).toHaveTextContent('CN¥0.10')
  expect(dialog).toHaveTextContent('CN¥1.00')
  expect(
    within(dialog).queryByRole('button', { name: 'WeChat' })
  ).not.toBeInTheDocument()
  await user.click(
    within(dialog).getByRole('button', { name: 'Pay CN¥0.10 and continue' })
  )
  await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
  expect(post.mock.calls[0][1]).toEqual({
    payment_method: 'alipay',
    config_version: 1,
  })
})

test.each([
  { status: 'claimed' as const },
  { status: 'ineligible' as const, eligible: false },
  { enabled: false },
  { eligible: false },
])(
  'does not offer a new payment for unavailable eligibility %j',
  async (override) => {
    Object.assign(info, override)
    const post = vi.spyOn(api, 'post')
    const { client } = renderBanner()
    await waitFor(() =>
      expect(
        client.getQueriesData({ queryKey: BENEFIT_QUERY_KEY })[0]?.[1]
      ).toBeTruthy()
    )
    expect(
      screen.queryByRole('region', { name: 'New user recharge benefit' })
    ).not.toBeInTheDocument()
    expect(post).not.toHaveBeenCalled()
  }
)

test('an unavailable payment method cannot start checkout', async () => {
  info.pay_methods = []
  const post = vi.spyOn(api, 'post')
  renderBanner()
  const dialog = await openDialog()
  expect(
    within(dialog).getByRole('button', { name: 'Pay CN¥0.10 and continue' })
  ).toBeDisabled()
  expect(dialog).toHaveTextContent(
    'This benefit payment method is currently unavailable.'
  )
  expect(post).not.toHaveBeenCalled()
})

test('the wallet keeps an entry after dismissing the global banner', async () => {
  const view = renderBanner()
  await view.user.click(
    await screen.findByRole('button', {
      name: 'Dismiss recharge benefit banner',
    })
  )
  expect(
    screen.queryByRole('region', { name: 'New user recharge benefit' })
  ).not.toBeInTheDocument()
  view.persistent()
  expect(
    await screen.findByRole('button', { name: 'View recharge benefit' })
  ).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Dismiss recharge benefit banner' })
  ).not.toBeInTheDocument()
})

test('switching accounts discards a previous account checkout response', async () => {
  let complete: ((value: unknown) => void) | undefined
  const post = vi.spyOn(api, 'post').mockImplementation(
    () =>
      new Promise((resolve) => {
        complete = resolve
      })
  )
  const { user } = renderBanner()
  const dialog = await openDialog()
  await user.click(
    within(dialog).getByRole('button', { name: 'Pay CN¥0.10 and continue' })
  )
  await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
  act(() =>
    useAuthStore
      .getState()
      .auth.setUser({ id: 8, username: 'different', role: 1 })
  )
  await act(async () =>
    complete?.({ data: { success: true, data: checkout() } })
  )
  expect(submitted).toEqual([])
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
})

test('returning after payment refreshes eligibility without triggering a payment', async () => {
  const post = vi.spyOn(api, 'post')
  renderBanner()
  await screen.findByRole('button', { name: 'View recharge benefit' })
  act(() => focusManager.setFocused(false))
  info.status = 'claimed'
  info.eligible = false
  act(() => focusManager.setFocused(true))
  await waitFor(() =>
    expect(
      screen.queryByRole('region', { name: 'New user recharge benefit' })
    ).not.toBeInTheDocument()
  )
  expect(post).not.toHaveBeenCalled()
})

function WalletBalanceProbe() {
  const wallet = useWalletSummary()
  return (
    <output aria-label='Wallet balance'>
      {wallet.summary?.total ?? 'loading'}
    </output>
  )
}

test('a pending-to-claimed transition refreshes the visible wallet balance once after the callback', async () => {
  info.status = 'pending'
  info.eligible = false
  info.pending_order = {
    order_id: 'benefit-order-1',
    payment_method: 'alipay',
    pay_amount_cny: '0.10',
    credit_amount_cny: '1.00',
    config_version: 1,
  }
  let balance = 0
  let walletReads = 0
  vi.mocked(api.get).mockImplementation(async (url) => {
    if (url === '/api/user/wallet') {
      walletReads += 1
      return {
        data: {
          success: true,
          data: { total: balance, paid: 0, bonus: balance, reward: 0 },
        },
      }
    }
    return { data: { success: true, data: structuredClone(info) } }
  })
  const client = createAppQueryClient()
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <NewUserBenefitBanner />
      <WalletBalanceProbe />
    </QueryClientProvider>
  )
  await screen.findByRole('button', { name: 'Continue benefit payment' })
  await waitFor(() =>
    expect(screen.getByLabelText('Wallet balance')).toHaveTextContent('0')
  )
  expect(walletReads).toBe(1)
  act(() => focusManager.setFocused(false))
  info.status = 'claimed'
  balance = 71429
  act(() => focusManager.setFocused(true))
  await waitFor(() =>
    expect(screen.getByLabelText('Wallet balance')).toHaveTextContent('71429')
  )
  expect(walletReads).toBe(2)
  act(() => focusManager.setFocused(false))
  act(() => focusManager.setFocused(true))
  await waitFor(() => expect(client.isFetching()).toBe(0))
  expect(walletReads).toBe(2)
})

test('a successful response with changed amounts never submits a payment form and refreshes the fixed method', async () => {
  const post = vi
    .spyOn(api, 'post')
    .mockImplementationOnce(async () => {
      info = {
        ...info,
        status: 'pending',
        eligible: false,
        enabled: false,
        pay_methods: [{ type: 'wxpay', name: 'WeChat' }],
        pending_order: {
          order_id: 'benefit-order-1',
          payment_method: 'wxpay',
          config_version: 2,
          pay_amount_cny: '0.25',
          credit_amount_cny: '2.00',
        },
      }
      return { data: { success: true, data: checkout('0.25', '2.00') } }
    })
    .mockResolvedValueOnce({
      data: { success: true, data: checkout('0.25', '2.00') },
    })
  const { user } = renderBanner()
  const dialog = await openDialog()
  await user.click(
    within(dialog).getByRole('button', { name: 'Pay CN¥0.10 and continue' })
  )
  expect(await screen.findByRole('button', { name: 'WeChat' })).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  expect(submitted).toEqual([])
  expect(post).toHaveBeenCalledTimes(1)
  await user.click(
    screen.getByRole('button', { name: 'Pay CN¥0.25 and continue' })
  )
  await waitFor(() => expect(post).toHaveBeenCalledTimes(2))
  expect(post.mock.calls[1][1]).toEqual({
    payment_method: 'wxpay',
    config_version: 2,
  })
})

test('the production query client leaves a business error to the localized feature handler exactly once', async () => {
  const errorToast = vi.spyOn(toast, 'error')
  vi.spyOn(api, 'post').mockRejectedValue(
    Object.assign(new Error('Raw provider failure'), {
      response: {
        status: 503,
        data: {
          code: 'new_user_benefit_unavailable',
          message: 'Untranslated backend fallback',
        },
      },
    })
  )
  const { user } = renderBanner()
  const dialog = await openDialog()
  await user.click(
    within(dialog).getByRole('button', { name: 'Pay CN¥0.10 and continue' })
  )
  await waitFor(() => expect(errorToast).toHaveBeenCalledTimes(1))
  expect(errorToast).toHaveBeenCalledWith(
    'New user benefit is temporarily unavailable.'
  )
  expect(submitted).toEqual([])
})

test.each(['zhCN', 'zhTW', 'fr', 'ru', 'ja', 'vi', 'en'] as const)(
  'the real banner and confirmation translate in %s',
  async (language) => {
    const bundle = await loadChuanapiI18nOverride(language)
    const instance = createInstance()
    await instance.init({
      lng: language,
      resources: { [language]: bundle ?? {} },
      interpolation: { escapeValue: false },
    })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false, gcTime: Infinity } },
    })
    clients.push(client)
    render(
      <I18nextProvider i18n={instance}>
        <QueryClientProvider client={client}>
          <NewUserBenefitBanner />
        </QueryClientProvider>
      </I18nextProvider>
    )
    const button = await screen.findByRole('button', {
      name: instance.t('View recharge benefit'),
    })
    fireEvent.click(button)
    expect(screen.getByRole('alertdialog')).toHaveTextContent(
      instance.t(
        'One benefit per account before its first successful recharge. Amounts are in CNY.'
      )
    )
    expect(screen.getByRole('alertdialog').textContent).toMatch(/1[.,]00/)
    expect(screen.getByRole('alertdialog').textContent).not.toMatch(/\{\{/)
  }
)
