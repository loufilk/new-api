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
