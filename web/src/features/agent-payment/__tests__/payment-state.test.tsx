import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { act, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { AgentPaymentPage } from '../index'

const payment = {
  public_id: 'synthetic-order',
  name: 'Synthetic customer payment',
  quota: 1_000_000,
  sale_price_fen: 120,
  status: 'pending',
  expires_at: 2_000_000_000,
  created_at: 1_800_000_000,
  payment_url: 'https://payment.example.test',
  payment_params: '{}',
}

const previousConfig = useSystemConfigStore.getState().config

beforeEach(() => {
  window.history.replaceState(
    null,
    '',
    '/#token=synthetic-customer-payment-token-00000001'
  )
  useAuthStore.getState().auth.setUser(null)
  useSystemConfigStore.getState().setConfig({
    currency: { ...previousConfig.currency, quotaPerUnit: 1_000_000 },
  })
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: '' },
  })
  vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: payment },
  })
})

afterEach(() => {
  window.history.replaceState(null, '', '/')
  useSystemConfigStore.getState().setConfig(previousConfig)
})

async function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  client.setQueryData(['status'], { announcements_enabled: false })
  const root = createRootRoute()
  const route = createRoute({
    getParentRoute: () => root,
    path: '/test',
    component: () => <AgentPaymentPage publicID={payment.public_id} />,
  })
  const router = createRouter({
    routeTree: root.addChildren([route]),
    history: createMemoryHistory({ initialEntries: ['/test'] }),
  })
  await router.load()
  return render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
}

test('pending payment shows face value using the configured quota unit', async () => {
  await mount()

  expect(await screen.findByText('$1')).toBeVisible()
  expect(screen.getByRole('button', { name: 'Pay ¥1.20' })).toBeEnabled()
})

test('a quota-unit update refreshes the displayed payment face value', async () => {
  await mount()
  expect(await screen.findByText('$1')).toBeVisible()

  act(() => {
    useSystemConfigStore.getState().setConfig({
      currency: { ...previousConfig.currency, quotaPerUnit: 250_000 },
    })
  })

  expect(screen.getByText('$4')).toBeVisible()
})

test('paid unused code remains available for the customer to redeem', async () => {
  vi.mocked(api.post).mockResolvedValue({
    data: {
      success: true,
      data: { ...payment, status: 'paid', redemption_key: 'SYNTHETIC-CODE' },
    },
  })
  await mount()

  expect(await screen.findByText('Payment successful')).toBeVisible()
  expect(screen.getByText('SYNTHETIC-CODE')).toBeVisible()
  expect(
    screen.getByText(
      'Your redemption code is ready. Copy it to your account wallet.'
    )
  ).toBeVisible()
})

test('redeemed code is labelled as used instead of inviting another redemption', async () => {
  vi.mocked(api.post).mockResolvedValue({
    data: {
      success: true,
      data: {
        ...payment,
        status: 'redeemed',
        redemption_key: 'SYNTHETIC-CODE',
      },
    },
  })
  await mount()

  expect(await screen.findByText('Code redeemed')).toBeVisible()
  expect(screen.getByText('SYNTHETIC-CODE')).toBeVisible()
  expect(
    screen.queryByText(
      'Your redemption code is ready. Copy it to your account wallet.'
    )
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Pay ¥1.20' })
  ).not.toBeInTheDocument()
})

test('refunded payment remains unavailable even if the response contains a stale key', async () => {
  vi.mocked(api.post).mockResolvedValue({
    data: {
      success: true,
      data: {
        ...payment,
        status: 'refunded',
        redemption_key: 'SYNTHETIC-CODE',
      },
    },
  })
  await mount()

  expect(
    await screen.findByText('Payment link is no longer available')
  ).toBeVisible()
  expect(screen.queryByText('SYNTHETIC-CODE')).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Pay ¥1.20' })
  ).not.toBeInTheDocument()
})
