import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { SimpleAgentFinance } from '../simple-finance'

test('root can record an ordinary user withdrawal even when there are no agent accounts', async () => {
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
      data: { items: [], total: 0, page: 1, page_size: 100 },
    },
  })
  const request = vi
    .spyOn(api, 'request')
    .mockResolvedValue({ data: { success: true, data: {} } })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SimpleAgentFinance />
    </QueryClientProvider>
  )
  fireEvent.click(
    await screen.findByRole('button', { name: 'Record user withdrawal' })
  )
  for (const [name, value] of [
    ['User ID', '73'],
    ['Referral reward ($)', '20.50'],
    ['Payment-code profit ($)', '0'],
    ['Reason', 'Synthetic support payout'],
    ['Support evidence', 'Synthetic payment reference'],
  ]) {
    fireEvent.change(screen.getByRole('textbox', { name }), {
      target: { value },
    })
  }
  expect(request).not.toHaveBeenCalled()
  fireEvent.click(
    screen.getByRole('checkbox', {
      name: 'I confirm the manual payment is complete. Record it and deduct the balance.',
    })
  )
  fireEvent.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(request).toHaveBeenCalledOnce())
  expect(request.mock.calls[0][0]).toMatchObject({
    url: '/api/agent-platform/simple/finance/withdrawal',
    data: { user_id: 73, referral_quota: 10_250_000, payment_quota: 0 },
  })
})

test('root sees the precise debt and can record a one-fen payment to settle it', async () => {
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...useSystemConfigStore.getState().config.currency,
      quotaPerUnit: 500_000,
    },
  })
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        items: [
          {
            agent_user_id: 73,
            username: 'Andy',
            status: 'paused',
            available_quota: 0,
            available_credit_quota: 3000,
            amount_due_quota: 2000,
            overdue_quota: 0,
          },
        ],
        total: 1,
      },
    },
  })
  const request = vi
    .spyOn(api, 'request')
    .mockResolvedValue({ data: { success: true, data: {} } })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SimpleAgentFinance />
    </QueryClientProvider>
  )
  fireEvent.click(await screen.findByRole('button', { name: 'Repayment' }))
  expect(screen.getByText(/Amount due: ¥0.004/)).toBeVisible()
  expect(
    screen.getByRole('textbox', { name: 'Repayment amount (CNY)' })
  ).toHaveValue('0.01')
  fireEvent.change(screen.getByRole('textbox', { name: 'Reason' }), {
    target: { value: 'Synthetic cash payment' },
  })
  fireEvent.click(
    screen.getByRole('checkbox', {
      name: 'I confirm this change and its financial impact',
    })
  )
  fireEvent.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(request).toHaveBeenCalledOnce())
  expect(request.mock.calls[0][0]).toMatchObject({
    url: '/api/agent-platform/simple/finance/repayment',
    data: { agent_user_id: 73, quota: 5000 },
  })
})

test('root can settle an entire fractional balance without rounding it through text input', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        items: [
          {
            agent_user_id: 73,
            username: 'Andy',
            status: 'paused',
            available_quota: 555,
            available_credit_quota: 0,
            amount_due_quota: 0,
          },
        ],
        total: 1,
      },
    },
  })
  const request = vi
    .spyOn(api, 'request')
    .mockResolvedValue({ data: { success: true, data: {} } })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SimpleAgentFinance />
    </QueryClientProvider>
  )
  fireEvent.click(
    await screen.findByRole('button', { name: 'Settle remaining balance' })
  )
  fireEvent.click(
    screen.getByRole('checkbox', { name: 'Settle all available balance' })
  )
  for (const [name, value] of [
    ['Reason', 'Exact fractional settlement'],
    ['Support evidence', 'Synthetic support reference'],
  ]) {
    fireEvent.change(screen.getByRole('textbox', { name }), {
      target: { value },
    })
  }
  fireEvent.click(
    screen.getByRole('checkbox', {
      name: 'I confirm this change and its financial impact',
    })
  )
  fireEvent.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(request).toHaveBeenCalledOnce())
  expect(request.mock.calls[0][0]).toMatchObject({
    url: '/api/agent-platform/simple/finance/exit',
    data: {
      agent_user_id: 73,
      quota: 0,
      settle_all: true,
      source: 'cash',
      target: 'wallet',
    },
  })
})
