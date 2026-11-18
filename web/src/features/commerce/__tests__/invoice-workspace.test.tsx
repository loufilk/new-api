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
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactElement } from 'react'
import { afterEach, describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { InvoiceApplicationForm } from '../components/invoice-application-form'
import { InvoicesPage } from '../invoices'
import type {
  CommerceConfig,
  InvoiceApplication,
  InvoiceOrder,
  InvoicePaymentOptions,
} from '../types'

const config: CommerceConfig = {
  revision: 1,
  invoice_enabled: true,
  invoice_window_days: 30,
  invoice_max_orders: 100,
  invoice_currency: 'CNY',
  general_fee_bps: 0,
  special_invoice_enabled: true,
  special_fee_bps: 0,
  invoice_instructions: '',
  partner_enabled: true,
  partner_instructions: '',
  updated_at: 0,
  updated_by: 0,
}
const orders: InvoiceOrder[] = [
  {
    source_type: 'topup',
    topup_id: 71,
    redemption_id: 0,
    trade_no: 'TOPUP-71',
    currency: 'CNY',
    currency_exponent: 2,
    amount_minor: '5000',
    completed_at: 1,
  },
  {
    source_type: 'redemption',
    topup_id: 0,
    redemption_id: 71,
    trade_no: 'RDM71',
    currency: 'CNY',
    currency_exponent: 2,
    amount_minor: '500',
    completed_at: 2,
  },
]
const paymentOptions: InvoicePaymentOptions = {
  methods: [
    { type: 'wallet', name: 'Balance', provider: 'wallet' },
    { type: 'wxpay', name: 'WeChat Pay', provider: 'epay' },
  ],
  wallet_quota: '1000000',
  wallet_balance_quota: '5000000',
  currency: 'CNY',
  currency_exponent: 2,
  fee_minor: '200',
}
function mount(element: ReactElement) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return {
    ...render(
      <QueryClientProvider client={client}>{element}</QueryClientProvider>
    ),
    client,
  }
}
function setBuyer() {
  useAuthStore.getState().auth.setUser({
    id: 21,
    username: 'buyer',
    role: 1,
    email: 'buyer@example.com',
  })
}
afterEach(() => useAuthStore.getState().auth.reset())

describe('invoice workspace', () => {
  test('switching from a personal title to a special invoice selects a company and exposes required registration fields', async () => {
    const user = userEvent.setup()
    const submit = vi.fn()
    mount(
      <InvoiceApplicationForm
        config={config}
        orders={orders}
        pending={false}
        onSubmit={submit}
      />
    )
    await user.click(screen.getByRole('button', { name: 'Individual' }))
    expect(
      screen.queryByLabelText('Tax identification number')
    ).not.toBeInTheDocument()
    await user.click(
      screen.getByRole('button', { name: 'VAT special invoice' })
    )
    expect(screen.getByRole('button', { name: 'Company' })).toHaveAttribute(
      'aria-pressed',
      'true'
    )
    expect(screen.getByRole('button', { name: 'Individual' })).toBeDisabled()
    expect(screen.getByLabelText('Tax identification number')).toBeRequired()
    expect(screen.getByLabelText('Registered address')).toBeRequired()
    expect(screen.getByLabelText('Registered phone')).toBeRequired()
    expect(screen.getByLabelText('Bank name')).toBeRequired()
    expect(screen.getByLabelText('Bank account')).toBeRequired()
    await user.click(
      screen.getByRole('checkbox', { name: /Select all eligible orders/ })
    )
    await user.click(screen.getByRole('button', { name: 'Submit application' }))
    expect(submit).not.toHaveBeenCalled()
    expect(screen.getByLabelText('Invoice title')).toHaveAttribute(
      'aria-invalid',
      'true'
    )
    expect(screen.getByLabelText('Registered address')).toHaveAttribute(
      'aria-invalid',
      'true'
    )
  })

  test('select-all caps the selection and keeps recharge and redemption identifiers separate', async () => {
    const user = userEvent.setup()
    const submit = vi.fn().mockResolvedValue(undefined)
    setBuyer()
    mount(
      <InvoiceApplicationForm
        config={{ ...config, invoice_max_orders: 2 }}
        orders={[
          ...orders,
          { ...orders[0], topup_id: 72, trade_no: 'TOPUP-72' },
        ]}
        pending={false}
        onSubmit={submit}
      />
    )
    await user.click(
      screen.getByRole('checkbox', { name: /Select all eligible orders/ })
    )
    expect(screen.getByRole('checkbox', { name: /TOPUP-71/ })).toBeChecked()
    expect(screen.getByRole('checkbox', { name: /RDM71/ })).toBeChecked()
    expect(screen.getByRole('checkbox', { name: /TOPUP-72/ })).toHaveAttribute(
      'aria-disabled',
      'true'
    )
    expect(screen.getByText('2 orders selected')).toBeVisible()
    await user.type(screen.getByLabelText('Invoice title'), 'Example Company')
    await user.type(
      screen.getByLabelText('Tax identification number'),
      'TAX-71'
    )
    await user.click(screen.getByRole('combobox', { name: 'Invoice category' }))
    await user.click(
      await screen.findByRole('option', {
        name: 'Software development services',
      })
    )
    await user.click(screen.getByRole('button', { name: 'Submit application' }))
    await waitFor(() => expect(submit).toHaveBeenCalledOnce())
    expect(submit.mock.calls[0][0]).toMatchObject({
      order_ids: [71],
      redemption_ids: [71],
      category: 'software_development',
    })
  })

  test('selection prevents mixing currency precision before displaying a combined amount or quoting a fee', async () => {
    const user = userEvent.setup()
    const highPrecision = {
      ...orders[0],
      topup_id: 72,
      trade_no: 'MILLI-72',
      currency_exponent: 3,
      amount_minor: '12345',
    }
    mount(
      <InvoiceApplicationForm
        config={config}
        orders={[...orders, highPrecision]}
        pending={false}
        onSubmit={vi.fn()}
      />
    )
    await user.click(
      screen.getByRole('checkbox', { name: /Select all eligible orders/ })
    )
    expect(screen.getByRole('checkbox', { name: /MILLI-72/ })).not.toBeChecked()
    expect(screen.getByRole('checkbox', { name: /MILLI-72/ })).toHaveAttribute(
      'aria-disabled',
      'true'
    )
    expect(screen.getByText('2 orders selected')).toBeVisible()
    expect(
      screen.getByText(
        'Orders in a different currency or precision require a separate application.'
      )
    ).toBeVisible()
  })

  test('refresh removes an unavailable selected order while preserving the invoice draft', async () => {
    const user = userEvent.setup()
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const submit = vi.fn()
    const view = (current: InvoiceOrder[]) => (
      <QueryClientProvider client={client}>
        <InvoiceApplicationForm
          config={config}
          orders={current}
          pending={false}
          onSubmit={submit}
        />
      </QueryClientProvider>
    )
    const result = render(view(orders))
    await user.click(screen.getByRole('checkbox', { name: /TOPUP-71/ }))
    await user.type(screen.getByLabelText('Invoice title'), 'Preserved draft')
    result.rerender(view([orders[1]]))
    expect(screen.getByText('0 orders selected')).toBeVisible()
    expect(
      screen.getByRole('button', { name: 'Submit application' })
    ).toBeDisabled()
    expect(screen.getByLabelText('Invoice title')).toHaveValue(
      'Preserved draft'
    )
    expect(submit).not.toHaveBeenCalled()
  })

  test('an insufficient paid balance disables wallet payment and allows selecting an available online method', async () => {
    const user = userEvent.setup()
    const submit = vi.fn().mockResolvedValue(undefined)
    setBuyer()
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: { ...paymentOptions, wallet_balance_quota: '10' },
      },
    })
    mount(
      <InvoiceApplicationForm
        config={{ ...config, general_fee_bps: 400 }}
        orders={[orders[0]]}
        pending={false}
        onSubmit={submit}
      />
    )
    await user.click(screen.getByRole('checkbox', { name: /TOPUP-71/ }))
    expect(
      await screen.findByText(
        'Paid balance is insufficient. Select another payment method or recharge your wallet.'
      )
    ).toBeVisible()
    expect(
      screen.getByRole('button', { name: 'Submit and pay tax fee' })
    ).toBeDisabled()
    await user.click(screen.getByRole('button', { name: 'WeChat Pay' }))
    await user.type(screen.getByLabelText('Invoice title'), 'Example Company')
    await user.type(
      screen.getByLabelText('Tax identification number'),
      'TAX-71'
    )
    await user.click(
      screen.getByRole('button', { name: 'Submit and pay tax fee' })
    )
    await waitFor(() => expect(submit).toHaveBeenCalledOnce())
    expect(submit.mock.calls[0][2]).toEqual({
      method: 'wxpay',
      expectedWalletQuota: undefined,
    })
  })

  test('a failed payment keeps the created application available for retry without creating another invoice', async () => {
    const user = userEvent.setup()
    setBuyer()
    let created = false
    let paid = false
    const invoice: InvoiceApplication = {
      id: 32,
      user_id: 21,
      kind: 'general',
      title_type: 'company',
      title: 'Example Company',
      tax_id: 'TAX-71',
      email: 'buyer@example.com',
      address: '',
      phone: '',
      bank_name: '',
      bank_account: '',
      category: 'technical_service',
      currency: 'CNY',
      currency_exponent: 2,
      amount_minor: '5000',
      fee_minor: '200',
      fee_bps: 400,
      fee_receipt_reference: '',
      fee_refund_status: '',
      fee_refund_reference: '',
      status: 'awaiting_fee',
      revision: 1,
      review_note: '',
      invoice_number: '',
      delivery_url: '',
      created_at: 1,
      updated_at: 1,
      orders: [orders[0]],
      events: [],
    }
    vi.spyOn(api, 'get').mockImplementation(async (url) => {
      if (String(url).includes('payment-options')) {
        return { data: { success: true, data: paymentOptions } }
      }
      if (url === '/api/invoices/eligible-orders') {
        return {
          data: {
            success: true,
            data: {
              config: { ...config, general_fee_bps: 400 },
              items: created ? [] : [orders[0]],
              summary: {
                currency: 'CNY',
                currency_exponent: 2,
                available: {
                  amount_minor: created ? '0' : '5000',
                  count: created ? 0 : 1,
                },
                processing: {
                  amount_minor: created ? '5000' : '0',
                  count: created ? 1 : 0,
                },
                issued: { amount_minor: '0', count: 0 },
              },
            },
          },
        }
      }
      const row = paid
        ? { ...invoice, status: 'submitted', revision: 2 }
        : invoice
      if (url === '/api/invoices/32') {
        return { data: { success: true, data: row } }
      }
      return {
        data: {
          success: true,
          data: { items: created ? [row] : [], total: created ? 1 : 0 },
        },
      }
    })
    let payAttempts = 0
    const post = vi.spyOn(api, 'post').mockImplementation(async (url) => {
      if (url === '/api/invoices') {
        created = true
        return { data: { success: true, data: invoice } }
      }
      payAttempts++
      if (payAttempts === 1) throw new Error('Payment connection interrupted')
      paid = true
      return {
        data: {
          success: true,
          data: {
            invoice: { ...invoice, status: 'submitted', revision: 2 },
            payment: { method: 'wallet', provider: 'wallet', status: 'paid' },
          },
        },
      }
    })
    mount(<InvoicesPage />)
    await user.click(await screen.findByRole('checkbox', { name: /TOPUP-71/ }))
    await screen.findByRole('button', { name: 'Balance' })
    await user.type(screen.getByLabelText('Invoice title'), 'Example Company')
    await user.type(
      screen.getByLabelText('Tax identification number'),
      'TAX-71'
    )
    await user.click(
      screen.getByRole('button', { name: 'Submit and pay tax fee' })
    )
    const dialog = await screen.findByRole('dialog', {
      name: 'Invoice application #32',
    })
    await waitFor(() => expect(payAttempts).toBe(1))
    await user.click(
      await within(dialog).findByRole('button', {
        name: 'Pay tax fee',
      })
    )
    await waitFor(() => expect(payAttempts).toBe(2))
    expect(
      post.mock.calls.filter((call) => call[0] === '/api/invoices')
    ).toHaveLength(1)
    const paymentRequests = post.mock.calls.filter(
      (call) => call[0] === '/api/invoices/32/pay'
    )
    expect(paymentRequests[0][2]).toEqual(paymentRequests[1][2])
    expect(paymentRequests[1][1]).toMatchObject({
      method: 'wallet',
      expected_wallet_quota: '1000000',
      expected_revision: 1,
    })
    await waitFor(() =>
      expect(
        within(dialog).queryByRole('button', {
          name: 'Pay tax fee',
        })
      ).not.toBeInTheDocument()
    )
  })
})
