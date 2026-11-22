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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import type { InvoiceDetail } from '../../contracts'
import { FeePayment } from '../payment'
import type { FeePaymentMethod } from '../policy'

const detail: InvoiceDetail = {
  id: 20,
  user_id: 3,
  status: 'awaiting_payment',
  currency: 'CNY',
  amount_minor: '200',
  order_count: 1,
  invoice_type: 'ordinary',
  title_type: 'company',
  title: 'Example',
  category: 'technical_service',
  email: 'test@example.invalid',
  created_at: 1,
  updated_at: 1,
  has_document: false,
  orders: [],
  fee_minor: '25',
  wallet_fee_quota: '125000',
  wallet_quota_per_cent: 5000,
  fee_state: 'unpaid',
  fee_rail: '',
}
const methods: FeePaymentMethod[] = [
  { id: 'paid_wallet', name: 'Paid balance', rail: 'wallet' },
  { id: 'wxpay', name: 'WeChat Pay', rail: 'epay' },
  { id: 'alipay', name: 'Alipay', rail: 'epay' },
]
function show(data: InvoiceDetail) {
  useAuthStore.getState().auth.setUser({ id: 3, role: 1, username: 'example' })
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <FeePayment
        owner={3}
        detail={data}
        methods={methods}
        disabled={false}
        onBusy={vi.fn()}
      />
    </QueryClientProvider>
  )
}
afterEach(() => {
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})

test('wallet debits only after selecting it and explicitly confirming the exact fee and paid quota', async () => {
  const user = userEvent.setup()
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      data: {
        ...detail,
        status: 'pending',
        fee_state: 'paid',
        fee_rail: 'wallet',
        fee_paid_minor: '25',
      },
    },
  })
  show(detail)
  expect(
    screen.getByRole('combobox', { name: 'Fee payment method' })
  ).toHaveValue('')
  expect(
    screen.getByRole('button', { name: 'Choose a payment method' })
  ).toBeDisabled()
  await user.selectOptions(screen.getByRole('combobox'), 'wallet:paid_wallet')
  expect(screen.getByText(/not bonus or reward quota/)).toBeVisible()
  expect(post).not.toHaveBeenCalled()
  await user.click(
    screen.getByRole('button', {
      name: /125000 paid quota units to pay .*0.25/,
    })
  )
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/invoice/applications/20/fee/wallet',
      {
        fee_minor: '25',
        wallet_quota: '125000',
        wallet_quota_per_cent: 5000,
      },
      { singleUseAuthorization: true }
    )
  )
  expect(post).toHaveBeenCalledTimes(1)
})

test('an inconsistent wallet quote cannot be selected, without blocking valid external payment choices', () => {
  show({ ...detail, wallet_fee_quota: '125001' })
  expect(
    screen.queryByRole('option', { name: 'Paid wallet balance' })
  ).not.toBeInTheDocument()
  expect(screen.getByRole('option', { name: 'WeChat Pay' })).toBeEnabled()
})

test('an open Epay checkout can resume only its pinned channel, never starts on view, and posts the matching signed form', async () => {
  const user = userEvent.setup()
  const params = { money: '0.25', type: 'wxpay', sign: 'synthetic-signature' }
  const formSubmit = vi
    .spyOn(HTMLFormElement.prototype, 'submit')
    .mockImplementation(() => {})
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      data: { url: 'https://payment.example.invalid/submit.php', params },
    },
  })
  show({
    ...detail,
    fee_state: 'checkout',
    fee_rail: 'epay',
    fee_payment_method: 'wxpay',
  })
  expect(screen.getByRole('combobox')).toBeDisabled()
  expect(
    screen.queryByRole('option', { name: 'Alipay' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('option', { name: 'Paid wallet balance' })
  ).not.toBeInTheDocument()
  expect(post).not.toHaveBeenCalled()
  expect(formSubmit).not.toHaveBeenCalled()
  await user.click(
    screen.getByRole('button', { name: /Pay .*0.25 with WeChat Pay/ })
  )
  await waitFor(() => expect(formSubmit).toHaveBeenCalledOnce())
  const form = formSubmit.mock.contexts[0]
  if (!(form instanceof HTMLFormElement)) {
    throw new Error('Expected checkout to submit an HTML form')
  }
  expect(form.action).toBe('https://payment.example.invalid/submit.php')
  expect(form.method.toUpperCase()).toBe('POST')
  expect(Object.fromEntries(new FormData(form))).toEqual(params)
  expect(post).toHaveBeenCalledWith(
    '/api/invoice/applications/20/fee/epay',
    { payment_method: 'wxpay' },
    { singleUseAuthorization: true }
  )
})

test.each([
  { url: 'javascript:alert(1)', money: '0.25', type: 'wxpay' },
  { url: 'https://payment.example.invalid/', money: '0.26', type: 'wxpay' },
  { url: 'https://payment.example.invalid/', money: '0.25', type: 'alipay' },
])(
  'an untrusted or changed payment form never leaves the console: $url $money $type',
  async ({ url, money, type }) => {
    const user = userEvent.setup()
    const formSubmit = vi
      .spyOn(HTMLFormElement.prototype, 'submit')
      .mockImplementation(() => {})
    vi.spyOn(api, 'post').mockResolvedValue({
      data: { success: true, data: { url, params: { money, type } } },
    })
    show(detail)
    await user.selectOptions(screen.getByRole('combobox'), 'epay:wxpay')
    await user.click(
      screen.getByRole('button', { name: /Pay .*with WeChat Pay/ })
    )
    expect(await screen.findByRole('alert')).toHaveTextContent(
      /check the payment status/i
    )
    expect(formSubmit).not.toHaveBeenCalled()
  }
)
