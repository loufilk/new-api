import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { SimplePaymentRecords } from '../simple-payment-records'
import { SimpleRecords } from '../simple-records'

const row = {
  id: 19,
  agent_user_id: 42,
  public_id: 'synthetic',
  name: 'Customer order',
  quota: 500_000,
  sale_price_fen: 120,
  cost_price_fen: 90,
  fee_fen: 2,
  profit_quota: 140_000,
  status: 'paid',
  customer_url: '',
  expires_at: 0,
  paid_at: 1_800_000_000,
  delivered_at: 1_800_000_000,
  created_at: 1_800_000_000,
  matures_at: 1_800_432_000,
  profit_matured: false,
  redemption_id: 41,
}
function mount(component: React.ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>{component}</QueryClientProvider>
  )
}
beforeEach(() => {
  useAuthStore.getState().auth.setUser({ id: 42, username: 'agent', role: 1 })
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
})
test('shows per-order income and maturity and retains previous-page navigation on an empty page', async () => {
  vi.spyOn(api, 'get')
    .mockResolvedValueOnce({
      data: {
        success: true,
        data: { items: [row], total: 21, page: 1, page_size: 20 },
      },
    })
    .mockResolvedValueOnce({
      data: {
        success: true,
        data: { items: [], total: 0, page: 2, page_size: 20 },
      },
    })
  mount(<SimplePaymentRecords agentUserId={42} />)
  expect(await screen.findByText('Customer order')).toBeVisible()
  expect(screen.getByText(/\$0.28 · Profit frozen/)).toBeVisible()
  expect(screen.getByText('Expected arrival')).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: 'Next' }))
  expect(await screen.findByText('No customer payment records')).toBeVisible()
  expect(screen.getByRole('button', { name: 'Previous' })).toBeEnabled()
  expect(vi.mocked(api.get).mock.calls[1][1]).toMatchObject({
    params: { p: 2 },
  })
})
test('root records a completed refund with confirmation and evidence', async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role: 100 })
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: { items: [row], total: 1, page: 1, page_size: 20 },
    },
  })
  const request = vi
    .spyOn(api, 'request')
    .mockResolvedValue({ data: { success: true, data: {} } })
  mount(<SimplePaymentRecords agentUserId={42} />)
  fireEvent.click(
    await screen.findByRole('button', { name: 'Record customer refund' })
  )
  expect(request).not.toHaveBeenCalled()
  fireEvent.change(screen.getByRole('textbox', { name: 'Reason' }), {
    target: { value: 'Customer requested refund' },
  })
  fireEvent.change(screen.getByRole('textbox', { name: 'Support evidence' }), {
    target: { value: 'Synthetic external refund receipt' },
  })
  fireEvent.click(
    screen.getByRole('checkbox', {
      name: 'I confirm this change and its financial impact',
    })
  )
  const buttons = screen.getAllByRole('button', {
    name: 'Record customer refund',
  })
  const confirmButton = buttons.at(-1)
  if (!confirmButton) throw new Error('Expected refund confirmation button')
  fireEvent.click(confirmButton)
  await waitFor(() => expect(request).toHaveBeenCalledOnce())
  expect(request.mock.calls[0][0]).toMatchObject({
    url: '/api/agent-platform/simple/payment/refunds',
    data: { order_id: 19, evidence: 'Synthetic external refund receipt' },
  })
})
test('one method filter controls both direct codes and pending payment orders', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data: {
        items: String(url).endsWith('/payment-orders')
          ? [{ ...row, status: 'pending' }]
          : [],
        total: String(url).endsWith('/payment-orders') ? 1 : 0,
        page: 1,
        page_size: 20,
      },
    },
  }))
  mount(<SimpleRecords agentUserId={42} />)
  expect(await screen.findByText('Customer order')).toBeVisible()
  fireEvent.change(
    screen.getByRole('combobox', { name: 'Filter code issue method' }),
    { target: { value: 'direct' } }
  )
  await waitFor(() =>
    expect(screen.queryByText('Customer order')).not.toBeInTheDocument()
  )
  fireEvent.change(
    screen.getByRole('combobox', { name: 'Filter code issue method' }),
    { target: { value: 'payment' } }
  )
  expect(await screen.findByText('Customer order')).toBeVisible()
  expect(
    screen.queryByText('No redemption codes found')
  ).not.toBeInTheDocument()
})
