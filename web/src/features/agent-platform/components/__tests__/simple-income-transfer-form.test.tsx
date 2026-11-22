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
import { expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { SimpleIncomeTransferForm } from '../simple-income-transfer-form'

test('payment income transfers through its own source without touching referral rewards', async () => {
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
  const request = vi
    .spyOn(api, 'request')
    .mockResolvedValue({ data: { success: true, data: {} } })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const onClose = vi.fn()
  render(
    <QueryClientProvider client={client}>
      <SimpleIncomeTransferForm availableQuota={10_000_000} onClose={onClose} />
    </QueryClientProvider>
  )
  fireEvent.change(screen.getByRole('textbox', { name: 'Amount ($)' }), {
    target: { value: '5.25' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Transfer' }))
  await waitFor(() => expect(onClose).toHaveBeenCalledOnce())
  expect(request).toHaveBeenCalledOnce()
  expect(request.mock.calls[0][0]).toMatchObject({
    url: '/api/agent-platform/simple/payment/transfer',
    method: 'post',
    data: { quota: 2_625_000, request_id: expect.any(String) },
  })
})
