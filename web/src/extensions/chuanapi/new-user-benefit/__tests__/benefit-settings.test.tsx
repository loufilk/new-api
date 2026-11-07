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
import { type QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { toast, Toaster } from 'sonner'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { SettingsPageProvider } from '@/features/system-settings/components/settings-page-context'
import { api } from '@/lib/api'
import { ledgerReasonLabelKey } from '@/lib/ledger-labels'
import { createAppQueryClient } from '@/lib/query-client'
import { useAuthStore } from '@/stores/auth-store'
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'

import { RechargeHistoryButton } from '../../wallet/components/recharge-history-dialog'
import { NewUserBenefitSettings } from '../benefit-settings'
import type { NewUserBenefitConfig } from '../types'

let config: NewUserBenefitConfig
let clients: QueryClient[] = []

function Settings() {
  const [actions, setActions] = useState<HTMLDivElement | null>(null)
  return (
    <SettingsPageProvider actionsContainer={actions}>
      <div ref={setActions} />
      <NewUserBenefitSettings />
      <Toaster />
    </SettingsPageProvider>
  )
}

beforeEach(() => {
  config = {
    enabled: false,
    pay_amount_cny: '0.10',
    credit_amount_cny: '1.00',
    version: 1,
  }
  useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role: 100 })
  useSystemConfigStore
    .getState()
    .setConfig({ currency: { ...DEFAULT_CURRENCY_CONFIG } })
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: config },
  })
})

afterEach(() => {
  cleanup()
  clients.forEach((client) => client.clear())
  clients = []
  useAuthStore.getState().auth.reset()
})

function renderSettings(content = <Settings />) {
  const client = createAppQueryClient()
  client.setDefaultOptions({
    queries: { retry: false, gcTime: Infinity },
    mutations: { retry: false },
  })
  clients.push(client)
  return {
    user: userEvent.setup(),
    client,
    ...render(
      <QueryClientProvider client={client}>{content}</QueryClientProvider>
    ),
  }
}

test('the defaults are disabled and a single save atomically submits amounts and the expected version', async () => {
  const put = vi.spyOn(api, 'put').mockResolvedValue({
    data: {
      success: true,
      data: {
        ...config,
        enabled: true,
        pay_amount_cny: '0.20',
        credit_amount_cny: '2.00',
        version: 2,
      },
    },
  })
  const { user } = renderSettings()
  const enabled = await screen.findByRole('switch', {
    name: 'Enable new user recharge benefit',
  })
  expect(enabled).not.toBeChecked()
  expect(screen.getByLabelText('Payment amount (CNY)')).toHaveValue('0.10')
  expect(screen.getByLabelText('Total credit (CNY)')).toHaveValue('1.00')
  await user.click(enabled)
  fireEvent.change(screen.getByLabelText('Payment amount (CNY)'), {
    target: { value: '0.2' },
  })
  fireEvent.change(screen.getByLabelText('Total credit (CNY)'), {
    target: { value: '2' },
  })
  await user.click(
    screen.getByRole('button', { name: 'Save recharge benefit' })
  )
  await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
  expect(put).toHaveBeenCalledWith('/api/option/new-user-benefit', {
    enabled: true,
    pay_amount_cny: '0.20',
    credit_amount_cny: '2.00',
    version: 1,
  })
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Save recharge benefit' })
    ).toBeDisabled()
  )
})

test.each(['0', '-0.1', '0.001', 'NaN', 'Infinity', '1e3', '1000000.01', ''])(
  'invalid payment amount %j never reaches the server',
  async (value) => {
    const put = vi.spyOn(api, 'put')
    const { user } = renderSettings()
    const input = await screen.findByLabelText('Payment amount (CNY)')
    fireEvent.change(input, { target: { value } })
    await user.click(
      screen.getByRole('button', { name: 'Save recharge benefit' })
    )
    expect(
      await screen.findByText(
        'Enter a CNY amount from 0.01 to 1,000,000.00 with at most two decimal places.'
      )
    ).toBeVisible()
    expect(put).not.toHaveBeenCalled()
  }
)

test('the total credit includes the payment and cannot be lower', async () => {
  const put = vi.spyOn(api, 'put')
  const { user } = renderSettings()
  fireEvent.change(await screen.findByLabelText('Total credit (CNY)'), {
    target: { value: '0.09' },
  })
  await user.click(
    screen.getByRole('button', { name: 'Save recharge benefit' })
  )
  expect(
    await screen.findByText('Total credit must be at least the payment amount.')
  ).toBeVisible()
  expect(put).not.toHaveBeenCalled()
})

test('a configuration conflict preserves edits and reload is an explicit action', async () => {
  const errorToast = vi.spyOn(toast, 'error')
  const put = vi.spyOn(api, 'put').mockRejectedValue(
    Object.assign(new Error('Conflict'), {
      response: {
        status: 409,
        data: { code: 'new_user_benefit_config_changed' },
      },
    })
  )
  const { user } = renderSettings()
  const input = await screen.findByLabelText('Payment amount (CNY)')
  fireEvent.change(input, { target: { value: '0.25' } })
  await user.click(
    screen.getByRole('button', { name: 'Save recharge benefit' })
  )
  expect(
    await screen.findByText('Recharge benefit settings changed')
  ).toBeVisible()
  expect(input).toHaveValue('0.25')
  expect(errorToast).not.toHaveBeenCalled()
  expect(
    screen.getByRole('button', { name: 'Save recharge benefit' })
  ).toBeDisabled()
  vi.mocked(api.get).mockResolvedValue({
    data: {
      success: true,
      data: { ...config, pay_amount_cny: '0.50', version: 3 },
    },
  })
  await user.click(
    screen.getByRole('button', { name: 'Reload saved settings' })
  )
  await waitFor(() => expect(input).toHaveValue('0.50'))
  expect(
    screen.queryByText('Recharge benefit settings changed')
  ).not.toBeInTheDocument()
  expect(put).toHaveBeenCalledTimes(1)
})

test.each([1, 10])(
  'role %s cannot load or change the root-only configuration',
  (role) => {
    useAuthStore.getState().auth.setUser({ id: 2, username: 'other', role })
    const put = vi.spyOn(api, 'put')
    renderSettings()
    expect(api.get).not.toHaveBeenCalled()
    expect(put).not.toHaveBeenCalled()
    expect(screen.queryByRole('switch')).not.toBeInTheDocument()
  }
)

test.each([false, true])(
  'recharge history displays total CNY 1.00 and paid CNY 0.10 for benefit orders (admin=%s)',
  async (admin) => {
    vi.mocked(api.get).mockResolvedValue({
      data: {
        success: true,
        data: {
          total: 1,
          items: [
            {
              id: 10,
              user_id: 7,
              amount: 0,
              money: 0.1,
              trade_no: 'benefit-history-order',
              payment_method: 'alipay',
              create_time: 1735689600,
              status: 'success',
              new_user_benefit: true,
              new_user_benefit_credit_quota: 71429,
              new_user_benefit_credit_amount_cny: '1.00',
            },
          ],
        },
      },
    })
    const { user } = renderSettings(<RechargeHistoryButton admin={admin} />)
    expect(api.get).not.toHaveBeenCalled()
    await user.click(
      screen.getByRole('button', {
        name: admin ? 'All recharge records' : 'Recharge history',
      })
    )
    const row = await screen.findByRole('row', {
      name: /benefit-history-order/,
    })
    expect(within(row).getByText('New user recharge benefit')).toBeVisible()
    expect(within(row).getByText('CN¥1.00')).toBeVisible()
    expect(within(row).getByText('CN¥0.10')).toBeVisible()
    expect(row).not.toHaveTextContent('CN¥0.00')
    expect(api.get).toHaveBeenCalledWith(
      `${admin ? '/api/user/topup' : '/api/user/topup/self'}?p=1&page_size=10`
    )
    expect(ledgerReasonLabelKey('new_user_topup')).toBe(
      'New user recharge benefit'
    )
  }
)

test.each([
  { provider: 'epay', amount: 10, money: 73, expected: '$10' },
  { provider: 'creem', amount: 500000, money: 3, expected: '$1' },
  { provider: 'stripe', amount: 10, money: 7.3, expected: '$7.3' },
  { provider: 'waffo', amount: 20, money: 150, expected: '$20' },
  { provider: 'waffo_pancake', amount: 30, money: 190, expected: '$30' },
])(
  'normal $provider recharge history uses the actual credited unit',
  async (record) => {
    vi.mocked(api.get).mockResolvedValue({
      data: {
        success: true,
        data: {
          total: 1,
          items: [
            {
              id: 11,
              user_id: 7,
              amount: record.amount,
              money: record.money,
              payment_provider: record.provider,
              payment_method: record.provider,
              trade_no: 'normal-provider-order',
              create_time: 1735689600,
              status: 'success',
            },
          ],
        },
      },
    })
    const { user } = renderSettings(<RechargeHistoryButton />)
    await user.click(screen.getByRole('button', { name: 'Recharge history' }))
    const row = await screen.findByRole('row', {
      name: /normal-provider-order/,
    })
    const cells = within(row).getAllByRole('cell')
    expect(cells[1]).toHaveTextContent(record.expected)
    if (record.provider === 'stripe') expect(cells[2]).toHaveTextContent('—')
  }
)

test('Stripe external payment is displayed from verified currency and minor units', async () => {
  vi.mocked(api.get).mockResolvedValue({
    data: {
      success: true,
      data: {
        total: 1,
        items: [
          {
            id: 12,
            user_id: 7,
            amount: 10,
            money: 7.3,
            payment_provider: 'stripe',
            payment_method: 'stripe',
            paid_currency: 'USD',
            paid_currency_exponent: 2,
            paid_amount_minor: '995',
            trade_no: 'stripe-receipt-order',
            create_time: 1735689600,
            status: 'success',
          },
        ],
      },
    },
  })
  const { user } = renderSettings(<RechargeHistoryButton />)
  await user.click(screen.getByRole('button', { name: 'Recharge history' }))
  const row = await screen.findByRole('row', { name: /stripe-receipt-order/ })
  const cells = within(row).getAllByRole('cell')
  expect(cells[1]).toHaveTextContent('$7.3')
  expect(cells[2]).toHaveTextContent(/USD\s+9\.95/)
})

test('agent-specific orders identify their separate ledger instead of presenting normal wallet credit', async () => {
  vi.mocked(api.get).mockResolvedValue({
    data: {
      success: true,
      data: {
        total: 1,
        items: [
          {
            id: 13,
            user_id: 7,
            amount: 100,
            money: 90,
            agent_simple_topup: true,
            payment_provider: 'epay',
            payment_method: 'alipay',
            trade_no: 'agent-recharge-order',
            create_time: 1735689600,
            status: 'success',
          },
        ],
      },
    },
  })
  const { user } = renderSettings(<RechargeHistoryButton admin />)
  await user.click(screen.getByRole('button', { name: 'All recharge records' }))
  const row = await screen.findByRole('row', { name: /agent-recharge-order/ })
  expect(row).toHaveTextContent('Agent balance')
  expect(within(row).getAllByRole('cell')[1]).toHaveTextContent(
    'See the agent balance ledger'
  )
})
