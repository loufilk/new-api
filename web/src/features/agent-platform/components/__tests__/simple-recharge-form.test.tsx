import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { SimpleRechargeForm } from '../simple-recharge-form'

function mount(
  kind: 'recharge' | 'repayment' = 'recharge',
  amountDue = 4_525_000
) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <SimpleRechargeForm kind={kind} amountDue={amountDue} onClose={vi.fn()} />
    </QueryClientProvider>
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
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: [
        { payment_method: 'alipay', enabled: true, fee_bps: 0, version: 1 },
      ],
    },
  })
  vi.spyOn(HTMLFormElement.prototype, 'submit').mockImplementation(() => {})
})

test('a lost recharge response retries the same intent and preserves decimal input', async () => {
  vi.spyOn(api, 'request')
    .mockRejectedValueOnce(new Error('Synthetic response lost'))
    .mockResolvedValueOnce({
      data: {
        success: true,
        data: {
          url: 'https://payment.example.test',
          data: { money: '90.01' },
          quota: 50_005_000,
          pay_yuan: 90.01,
        },
      },
    })
  mount()
  const input = await screen.findByRole('textbox', {
    name: 'Agent balance to add ($)',
  })
  fireEvent.change(input, { target: { value: '100.01' } })
  fireEvent.click(screen.getByRole('button', { name: 'Continue to payment' }))
  await waitFor(() => expect(api.request).toHaveBeenCalledTimes(1))
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Continue to payment' })
    ).toBeEnabled()
  )
  fireEvent.click(screen.getByRole('button', { name: 'Continue to payment' }))
  await waitFor(() => expect(api.request).toHaveBeenCalledTimes(2))
  const calls = vi.mocked(api.request).mock.calls
  expect(calls[0][0].data).toEqual(calls[1][0].data)
  expect(calls[0][0].data).toMatchObject({
    amount: '100.01',
    kind: 'recharge',
    payment_method: 'alipay',
    request_id: expect.any(String),
  })
})

test('repayment defaults to the exact CNY amount due and requests repayment methods', async () => {
  vi.spyOn(api, 'request').mockResolvedValue({
    data: {
      success: true,
      data: {
        url: 'https://payment.example.test',
        data: { money: '9.05' },
        quota: 0,
        pay_yuan: 9.05,
      },
    },
  })
  mount('repayment')
  expect(
    await screen.findByRole('textbox', { name: 'Repayment amount (CNY)' })
  ).toHaveValue('9.05')
  expect(vi.mocked(api.get).mock.calls[0][1]).toMatchObject({
    params: { purpose: 'repayment' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Continue to payment' }))
  await waitFor(() => expect(api.request).toHaveBeenCalledOnce())
  expect(vi.mocked(api.request).mock.calls[0][0].data).toMatchObject({
    amount: '9.05',
    kind: 'repayment',
  })
})

test('excess precision is rejected before creating a payment intent', async () => {
  const request = vi.spyOn(api, 'request')
  mount()
  fireEvent.change(
    await screen.findByRole('textbox', { name: 'Agent balance to add ($)' }),
    { target: { value: '100.001' } }
  )
  fireEvent.click(screen.getByRole('button', { name: 'Continue to payment' }))
  expect(
    await screen.findByText(
      'Enter a positive amount with at most two decimal places.'
    )
  ).toBeVisible()
  expect(request).not.toHaveBeenCalled()
})

test.each([1, 2000, 4500])(
  'a debt of %d quota can be settled with a one-fen payment',
  async (amountDue) => {
    const request = vi.spyOn(api, 'request').mockResolvedValue({
      data: {
        success: true,
        data: { url: 'https://payment.example.test', data: { money: '0.01' } },
      },
    })
    mount('repayment', amountDue)
    expect(
      await screen.findByRole('textbox', { name: 'Repayment amount (CNY)' })
    ).toHaveValue('0.01')
    fireEvent.click(screen.getByRole('button', { name: 'Continue to payment' }))
    await waitFor(() => expect(request).toHaveBeenCalledOnce())
    expect(request.mock.calls[0][0].data).toMatchObject({
      amount: '0.01',
      kind: 'repayment',
    })
  }
)
