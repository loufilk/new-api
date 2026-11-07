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
import { QueryClientProvider, type QueryClient } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterContextProvider,
} from '@tanstack/react-router'
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
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { createAppQueryClient } from '@/lib/query-client'
import { useAuthStore } from '@/stores/auth-store'

import { NewUserBenefitBanner } from '../benefit-banner'
import { BENEFIT_QUERY_KEY } from '../lib'
import type { NewUserBenefitInfo } from '../types'

let info: NewUserBenefitInfo
let client: QueryClient
let widgets: Array<{ element: HTMLElement; options: Record<string, unknown> }>
const paymentResponse = {
  data: {
    success: true,
    data: {
      order_id: 'benefit-security-order',
      pay_amount_cny: '0.10',
      credit_amount_cny: '1.00',
      status: 'pending',
      checkout: {
        kind: 'form',
        url: 'https://payments.example/submit.php',
        params: { money: '0.10' },
      },
    },
  },
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
    blocked_reason: '',
    turnstile_required: true,
    turnstile_site_key: 'benefit-public-site-key',
  }
  widgets = []
  window.turnstile = {
    render: vi.fn((element, options) => {
      widgets.push({ element, options })
      return `benefit-widget-${widgets.length}`
    }),
    remove: vi.fn(),
  }
  useAuthStore
    .getState()
    .auth.setUser({ id: 7, username: 'benefit-security', role: 1 })
  client = createAppQueryClient()
  client.setDefaultOptions({
    queries: { retry: false, gcTime: Infinity },
    mutations: { retry: false },
  })
  vi.spyOn(api, 'get').mockImplementation(async () => ({
    data: { success: true, data: structuredClone(info) },
  }))
  vi.spyOn(HTMLFormElement.prototype, 'submit').mockImplementation(
    () => undefined
  )
})

afterEach(() => {
  cleanup()
  client.clear()
  useAuthStore.getState().auth.reset()
  delete window.turnstile
  document.querySelector('#cf-turnstile')?.remove()
})

function show(persistent = true) {
  const router = createRouter({
    routeTree: createRootRoute(),
    history: createMemoryHistory({ initialEntries: ['/wallet'] }),
  })
  render(
    <QueryClientProvider client={client}>
      <RouterContextProvider router={router}>
        <NewUserBenefitBanner persistent={persistent} />
      </RouterContextProvider>
    </QueryClientProvider>
  )
  return userEvent.setup()
}

async function openPayment() {
  fireEvent.click(
    await screen.findByRole('button', { name: 'View recharge benefit' })
  )
  const dialog = await screen.findByRole('alertdialog')
  return within(dialog).getByRole('button', {
    name: 'Pay CN¥0.10 and continue',
  })
}

function verify(token: string) {
  const widget = [...widgets]
    .reverse()
    .find((entry) => entry.element.isConnected)
  if (!widget) {
    throw new Error('Expected a mounted security verification widget')
  }
  expect(widget.options.action).toBe('new_user_benefit')
  act(() => (widget.options.callback as (value: string) => void)(token))
}

test('checkout waits for a server-required human check and sends its proof only in the query', async () => {
  const post = vi.spyOn(api, 'post').mockResolvedValue(paymentResponse)
  const user = show()
  const pay = await openPayment()
  expect(pay).toBeDisabled()
  expect(post).not.toHaveBeenCalled()
  verify('fresh-benefit-proof')
  expect(pay).toBeEnabled()
  await user.click(pay)
  await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
  expect(post.mock.calls[0][1]).toEqual({
    payment_method: 'alipay',
    config_version: 1,
  })
  expect(post.mock.calls[0][2]?.params).toEqual({
    turnstile: 'fresh-benefit-proof',
  })
})

test('a failed checkout consumes the local proof and a manual retry needs a new proof with the same order key', async () => {
  const post = vi
    .spyOn(api, 'post')
    .mockRejectedValueOnce(new Error('Payment network unavailable'))
    .mockResolvedValueOnce(paymentResponse)
  const user = show()
  const pay = await openPayment()
  verify('first-benefit-proof')
  await user.click(pay)
  await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
  await waitFor(() => expect(client.isMutating()).toBe(0))
  expect(screen.getByRole('alertdialog')).toBeVisible()
  expect(pay).toBeDisabled()
  verify('second-benefit-proof')
  await user.click(pay)
  await waitFor(() => expect(post).toHaveBeenCalledTimes(2))
  expect(post.mock.calls[1][2]?.params).toEqual({
    turnstile: 'second-benefit-proof',
  })
  expect(post.mock.calls[1][2]?.headers?.['Idempotency-Key']).toBe(
    post.mock.calls[0][2]?.headers?.['Idempotency-Key']
  )
})

test('an expired human check disables payment until the widget verifies again', async () => {
  const post = vi.spyOn(api, 'post')
  show()
  const pay = await openPayment()
  verify('expiring-benefit-proof')
  expect(pay).toBeEnabled()
  const widget = widgets.at(-1)
  if (!widget) {
    throw new Error('Expected a mounted security verification widget')
  }
  act(() => (widget.options['expired-callback'] as () => void)())
  expect(pay).toBeDisabled()
  expect(post).not.toHaveBeenCalled()
})

test('closing and reopening the confirmation discards its prior human verification', async () => {
  const user = show()
  await openPayment()
  verify('cancelled-benefit-proof')
  await user.click(screen.getByRole('button', { name: 'Cancel' }))
  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
  expect(await openPayment()).toBeDisabled()
})

test('the wallet explains missing email verification and links to the existing account security page', async () => {
  info.eligible = false
  info.status = 'ineligible'
  info.blocked_reason = 'email_verification_required'
  const post = vi.spyOn(api, 'post')
  show()
  const verifyLink = await screen.findByRole('link', {
    name: 'Verify email in account security',
  })
  expect(verifyLink).toHaveAttribute('href', '/security')
  expect(
    screen.getByRole('region', { name: 'New user recharge benefit' })
  ).toHaveTextContent(
    'Verify your email address before claiming the new user benefit'
  )
  expect(
    screen.queryByRole('button', { name: 'View recharge benefit' })
  ).not.toBeInTheDocument()
  expect(post).not.toHaveBeenCalled()
})

test.each([
  [
    'identity_already_used',
    'This account is not eligible for this recharge benefit.',
  ],
  [
    'limit_reached',
    'The new user benefit limit has been reached. Please try again later',
  ],
  [
    'verification_unavailable',
    'Security verification is temporarily unavailable. Please try again later.',
  ],
] as const)(
  'a wallet blocked by %s explains the server decision without offering checkout',
  async (reason, message) => {
    info.eligible = false
    info.status = 'ineligible'
    info.blocked_reason = reason
    const post = vi.spyOn(api, 'post')
    show()
    expect(await screen.findByText(message)).toBeVisible()
    expect(
      screen.queryByRole('button', { name: 'View recharge benefit' })
    ).not.toBeInTheDocument()
    expect(post).not.toHaveBeenCalled()
  }
)

test('a pending order remains payable without a new challenge when the server says verification is disabled', async () => {
  info.status = 'pending'
  info.enabled = false
  info.eligible = false
  info.turnstile_required = false
  info.turnstile_site_key = ''
  info.pending_order = {
    order_id: 'benefit-security-order',
    pay_amount_cny: '0.10',
    credit_amount_cny: '1.00',
    payment_method: 'alipay',
    config_version: 1,
  }
  const post = vi.spyOn(api, 'post').mockResolvedValue(paymentResponse)
  const user = show()
  await user.click(
    await screen.findByRole('button', { name: 'Continue benefit payment' })
  )
  const pay = screen.getByRole('button', { name: 'Pay CN¥0.10 and continue' })
  expect(pay).toBeEnabled()
  expect(widgets).toHaveLength(0)
  await user.click(pay)
  await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
})

test('a required verification without a site key fails closed instead of submitting payment', async () => {
  info.turnstile_site_key = ''
  const post = vi.spyOn(api, 'post')
  show()
  const pay = await openPayment()
  expect(pay).toBeDisabled()
  expect(screen.getByRole('alertdialog')).toHaveTextContent(
    'Security verification is temporarily unavailable. Please try again later.'
  )
  expect(post).not.toHaveBeenCalled()
})

test('changing the verification site key while confirmation is open invalidates the old proof', async () => {
  show()
  const pay = await openPayment()
  verify('old-site-proof')
  expect(pay).toBeEnabled()
  info.turnstile_site_key = 'replacement-public-site-key'
  await act(async () =>
    client.invalidateQueries({ queryKey: BENEFIT_QUERY_KEY })
  )
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Pay CN¥0.10 and continue' })
    ).toBeDisabled()
  )
})
