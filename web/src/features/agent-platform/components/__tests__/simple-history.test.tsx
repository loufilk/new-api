import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { SimpleHistory } from '../simple-history'

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
})
function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SimpleHistory agentUserId={42} />
    </QueryClientProvider>
  )
}

test('history shows funding sources with actor and reason and switches to separate withdrawal sources', async () => {
  vi.spyOn(api, 'get')
    .mockResolvedValueOnce({
      data: {
        success: true,
        data: {
          items: [
            {
              id: 1,
              agent_user_id: 42,
              actor_user_id: 1,
              created_at: 1_800_000_000,
              delta_cash_quota: 500_000,
              delta_credit_limit: 50_000_000,
              reason: 'Synthetic adjustment',
              reference_id: 'synthetic-ledger-1',
            },
          ],
          total: 1,
        },
      },
    })
    .mockResolvedValueOnce({
      data: {
        success: true,
        data: {
          items: [
            {
              id: 2,
              agent_user_id: 42,
              actor_user_id: 1,
              created_at: 1_800_000_000,
              referral_quota: 10_000_000,
              payment_quota: 5_000_000,
              fee_quota: 300_000,
              net_quota: 14_700_000,
              evidence: 'Synthetic payout receipt',
            },
          ],
          total: 1,
        },
      },
    })
  mount()
  expect(await screen.findByText('Reason: Synthetic adjustment')).toBeVisible()
  expect(screen.getByText(/Operator: 1/)).toBeVisible()
  expect(screen.getByText('$1')).toBeVisible()
  expect(screen.getByText('¥100.00')).toBeVisible()
  fireEvent.change(screen.getByRole('combobox', { name: 'History type' }), {
    target: { value: 'withdrawals' },
  })
  expect(await screen.findByText('$20')).toBeVisible()
  expect(screen.getByText('$10')).toBeVisible()
  expect(screen.getByText('$29.4')).toBeVisible()
  expect(
    screen.getByText('Support evidence: Synthetic payout receipt')
  ).toBeVisible()
  expect(vi.mocked(api.get).mock.calls[1][0]).toBe(
    '/api/agent-platform/simple/finance/history/withdrawals'
  )
})

test('paid credits waiting for account capacity remain visible in recharge records', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        items: [
          {
            id: 7,
            agent_simple_quota: 500_000,
            agent_simple_deferred_quota: 250_000,
            money: 0.9,
            status: 'success',
            payment_method: 'alipay',
          },
        ],
        total: 1,
      },
    },
  })
  mount()
  fireEvent.change(screen.getByRole('combobox', { name: 'History type' }), {
    target: { value: 'recharges' },
  })
  expect(
    await screen.findByText('Paid credit awaiting balance capacity')
  ).toBeVisible()
  expect(screen.getByText('$0.5')).toBeVisible()
})

test('weekly bill and daily reminder history show real due and reminder dates', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    let items: Record<string, string | number>[] = []
    if (String(url).endsWith('/bills')) {
      items = [
        {
          id: 3,
          agent_user_id: 42,
          created_at: 1_800_000_000,
          quota: 4_500_000,
          paid_quota: 2_250_000,
          due_at: 1_800_259_200,
        },
      ]
    } else if (String(url).endsWith('/reminders')) {
      items = [
        {
          id: 4,
          agent_user_id: 42,
          created_at: 1_800_300_000,
          quota: 2_250_000,
          day: '2027-01-18',
        },
      ]
    }
    return { data: { success: true, data: { items, total: 1 } } }
  })
  mount()
  fireEvent.change(screen.getByRole('combobox', { name: 'History type' }), {
    target: { value: 'bills' },
  })
  expect(await screen.findByText('Due date')).toBeVisible()
  expect(screen.getByText('¥9.00')).toBeVisible()
  expect(screen.getAllByText('¥4.50')).toHaveLength(2)
  fireEvent.change(screen.getByRole('combobox', { name: 'History type' }), {
    target: { value: 'reminders' },
  })
  expect(await screen.findByText('2027-01-18')).toBeVisible()
  await waitFor(() => expect(screen.getByText('¥4.50')).toBeVisible())
})
