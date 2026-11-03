import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { SimplePaymentForm } from '../simple-payment-form'

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
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: [
        { payment_method: 'alipay', enabled: true, fee_bps: 100, version: 2 },
      ],
    },
  })
})
function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SimplePaymentForm onClose={vi.fn()} />
    </QueryClientProvider>
  )
}
async function preview(price = '1.20') {
  fireEvent.change(await screen.findByRole('textbox', { name: 'Name' }), {
    target: { value: 'Customer payment' },
  })
  fireEvent.change(screen.getByRole('textbox', { name: 'Quota' }), {
    target: { value: '1' },
  })
  fireEvent.change(
    screen.getByRole('textbox', { name: 'Customer price (yuan)' }),
    { target: { value: price } }
  )
  fireEvent.click(
    screen.getByRole('button', { name: 'Preview price and income' })
  )
}
const quote = {
  quota: 500_000,
  sale_price_fen: 120,
  cost_price_fen: 90,
  fee_fen: 2,
  profit_quota: 140_000,
  minimum_sale_price_fen: 91,
  fee_version: 2,
  price_valid: true,
}

test('previews authoritative costs before creating a link and QR code', async () => {
  const request = vi
    .spyOn(api, 'request')
    .mockResolvedValueOnce({ data: { success: true, data: quote } })
    .mockResolvedValueOnce({
      data: {
        success: true,
        data: {
          order: {
            customer_url:
              'https://example.test/agent-pay/order#token=synthetic',
          },
          payment_url: 'https://payment.example.test',
          payment_params: {},
        },
      },
    })
  mount()
  await preview()
  expect(
    await screen.findByRole('heading', { name: 'Review customer payment' })
  ).toBeVisible()
  expect(screen.getByText('¥0.90')).toBeVisible()
  expect(screen.getByText('¥0.02')).toBeVisible()
  expect(screen.getByText('¥0.91')).toBeVisible()
  expect(screen.getByText('$0.28')).toBeVisible()
  expect(request).toHaveBeenCalledOnce()
  fireEvent.click(screen.getByRole('button', { name: 'Create payment link' }))
  expect(
    await screen.findByRole('img', { name: 'Customer payment QR code' })
  ).toBeVisible()
  expect(
    screen.getByText('https://example.test/agent-pay/order#token=synthetic')
  ).toBeVisible()
  expect(request.mock.calls[1][0]).toMatchObject({
    url: '/api/agent-platform/simple/payment',
    data: {
      quota: 500_000,
      sale_price_fen: 120,
      fee_version: 2,
      request_id: expect.any(String),
    },
  })
})

test('below-minimum preview blocks creation and edit keeps the entered values', async () => {
  const request = vi.spyOn(api, 'request').mockResolvedValue({
    data: {
      success: true,
      data: {
        ...quote,
        sale_price_fen: 90,
        fee_fen: 1,
        profit_quota: 0,
        price_valid: false,
      },
    },
  })
  mount()
  await preview('0.90')
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Minimum customer price is ¥0.91.'
  )
  expect(
    screen.getByRole('button', { name: 'Create payment link' })
  ).toBeDisabled()
  fireEvent.click(screen.getByRole('button', { name: 'Edit price' }))
  expect(await screen.findByRole('textbox', { name: 'Name' })).toHaveValue(
    'Customer payment'
  )
  expect(
    screen.getByRole('textbox', { name: 'Customer price (yuan)' })
  ).toHaveValue('0.90')
  await waitFor(() => expect(request).toHaveBeenCalledOnce())
})
