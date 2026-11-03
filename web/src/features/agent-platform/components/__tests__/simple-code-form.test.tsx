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
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { SimpleCodeForm } from '../simple-code-form'

function mount(node: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>{node}</QueryClientProvider>
  )
}

beforeEach(() => {
  useSystemConfigStore.getState().setConfig({
    currency: {
      displayInCurrency: true,
      customCurrencySymbol: '$',
      customCurrencyExchangeRate: 1,
      quotaDisplayType: 'USD',
      quotaPerUnit: 500_000,
      usdExchangeRate: 1,
    },
  })
  vi.spyOn(api, 'request').mockResolvedValue({
    data: {
      success: true,
      data: {
        id: 19,
        user_id: 42,
        name: 'Customer code',
        key: 'DELIVERY-SECRET',
        status: 1,
        quota: 500_000,
        created_time: 1_800_000_000,
        redeemed_time: 0,
        expired_time: 0,
        used_user_id: 0,
      },
    },
  })
})

test('creates one simple redemption code and keeps the credential visible for delivery', async () => {
  const onClose = vi.fn()
  mount(
    <SimpleCodeForm
      balance={{
        agent_user_id: 42,
        status: 'active',
        cash_quota: 1_000_000,
        reward_quota: 0,
        root_quota: 0,
        credit_reserved_quota: 0,
        credit_outstanding_quota: 0,
        payment_pending_quota: 0,
        payment_available_quota: 0,
        payment_total_quota: 0,
        payment_debt_quota: 0,
        available_quota: 1_000_000,
        available_credit_quota: 0,
        amount_due_quota: 0,
        reserved_quota: 0,
      }}
      onClose={onClose}
    />
  )

  fireEvent.change(screen.getByRole('textbox', { name: 'Name' }), {
    target: { value: 'Customer code' },
  })
  fireEvent.change(screen.getByRole('textbox', { name: 'Quota' }), {
    target: { value: '1' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Create code' }))

  expect(await screen.findByText('DELIVERY-SECRET')).toBeInTheDocument()
  expect(onClose).not.toHaveBeenCalled()
  expect(vi.mocked(api.request).mock.calls[0][0]).toMatchObject({
    method: 'post',
    url: '/api/agent-platform/simple/codes',
    data: {
      name: 'Customer code',
      quota: 500_000,
      mode: 'direct',
      expires_at: 0,
    },
  })
  await waitFor(() => {
    expect(vi.mocked(api.request).mock.calls[0][0].data).toHaveProperty(
      'request_id'
    )
  })
})
