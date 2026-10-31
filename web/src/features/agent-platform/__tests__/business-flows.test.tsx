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
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { DeliveryClaim } from '../claim'
import { Deliveries } from '../components/deliveries'
import { FundReconciliation } from '../components/fund-accounting'
import { FundBatches } from '../components/fund-batches'
import { PolicyPanel } from '../components/policy-panel'
import { PurchaseCreate } from '../components/purchase-create'
import { PurchaseRefunds } from '../components/purchase-refunds'
import { Purchases } from '../components/purchases'

const ok = (data: unknown) => ({ data: { success: true, data } })
const page = (items: unknown[]) => ({
  items,
  total: items.length,
  page: 1,
  page_size: 20,
})
const order = {
  id: 10,
  agent_user_id: 42,
  quota: 500000,
  paid_minor: '8800',
  currency: 'USD',
  currency_exponent: 2,
  status: 'verified',
  version: 2,
  policy_version: 1,
  verified_by: 43,
  reviewed_by: 0,
  created_at: 1700000000,
  evidence_reference: 'receipt-10',
}
const delivery = {
  id: 9,
  agent_user_id: 40,
  name: 'Fixed customer delivery',
  quota: 500000,
  state: 'claimed',
  version: 2,
  customer_user_id: 42,
  expires_at: 0,
  claimed_at: 1700000000,
}
const refund = {
  id: 3,
  agent_user_id: 42,
  batch_id: 1,
  quota: 500000,
  currency: 'USD',
  currency_exponent: 2,
  principal_minor: '8800',
  fee_minor: '80',
  net_minor: '8720',
  state: 'payment_unknown',
  business_key: 'agent-purchase-refund:3',
  version: 3,
  created_at: 1700000000,
}
async function mount(node: ReactNode, entry = '/test') {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const root = createRootRoute()
  const route = createRoute({
    getParentRoute: () => root,
    path: '/test',
    component: () => node,
  })
  const other = createRoute({
    getParentRoute: () => root,
    path: '/other',
    component: () => <p>Other page</p>,
  })
  const router = createRouter({
    routeTree: root.addChildren([route, other]),
    history: createMemoryHistory({ initialEntries: [entry] }),
  })
  await router.load()
  const view = render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  return { ...view, router }
}
beforeEach(() => {
  sessionStorage.clear()
  useAuthStore
    .getState()
    .auth.setUser({ id: 42, username: 'customer', role: 1 })
  vi.spyOn(window, 'scrollTo').mockImplementation(() => undefined)
  vi.spyOn(api, 'get').mockResolvedValue(ok(page([])))
  vi.spyOn(api, 'request').mockResolvedValue(ok({}))
})
describe('protected customer claim lifecycle', () => {
  test('fragment prefill is stripped without auto-claim; bind is separate from explicit redemption and cancellation keeps the claim', async () => {
    let claimed = false
    vi.mocked(api.get).mockImplementation(async () =>
      ok(page(claimed ? [delivery] : []))
    )
    vi.mocked(api.request).mockImplementation(async () => {
      claimed = true
      return ok(delivery)
    })
    const { router } = await mount(
      <DeliveryClaim />,
      '/test#code=bearer-secret'
    )
    const input = await screen.findByRole('textbox', { name: 'Claim code' })
    await waitFor(() => expect(input).toHaveValue('bearer-secret'))
    await waitFor(() => expect(router.state.location.hash).toBe(''))
    expect(api.request).not.toHaveBeenCalled()
    fireEvent.click(
      screen.getByRole('button', { name: 'Bind delivery to my account' })
    )
    await screen.findByRole('alertdialog', {
      name: 'Confirm redemption into your wallet',
    })
    expect(api.request).toHaveBeenCalledTimes(1)
    expect(vi.mocked(api.request).mock.calls[0][0]).toMatchObject({
      url: '/api/agent-platform/deliveries/claim',
      data: { claim_code: 'bearer-secret' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    await screen.findByRole('button', { name: 'Review and redeem' })
    expect(api.request).toHaveBeenCalledTimes(1)
    await act(async () => {
      router.history.push('/other')
      router.history.back()
    })
    await screen.findByRole('button', { name: 'Review and redeem' })
    expect(api.request).toHaveBeenCalledTimes(1)
    expect(JSON.stringify(sessionStorage)).not.toContain('bearer-secret')
  })
  test('a bound claim requires an explicit final click and prevents duplicate redemption submits', async () => {
    vi.mocked(api.get).mockResolvedValue(ok(page([delivery])))
    let finish!: (value: unknown) => void
    vi.mocked(api.request).mockImplementation(
      () =>
        new Promise((resolve) => {
          finish = resolve
        })
    )
    await mount(<DeliveryClaim />)
    fireEvent.click(
      await screen.findByRole('button', { name: 'Review and redeem' })
    )
    const button = screen.getByRole('button', { name: 'Redeem into my wallet' })
    fireEvent.click(button)
    fireEvent.click(button)
    await waitFor(() => expect(api.request).toHaveBeenCalledTimes(1))
    expect(vi.mocked(api.request).mock.calls[0][0]).toMatchObject({
      url: '/api/agent-platform/deliveries/9/confirm',
      data: { delivery_id: 9, version: 2 },
    })
    await act(async () => finish(ok({ id: 9, state: 'redeemed' })))
  })
  test('a denied claim does not display a redemption confirmation or credit a wallet', async () => {
    vi.mocked(api.request).mockResolvedValue({
      data: {
        success: false,
        message: 'This delivery is bound to a different customer',
      },
    })
    await mount(<DeliveryClaim />)
    fireEvent.change(
      await screen.findByRole('textbox', { name: 'Claim code' }),
      { target: { value: 'another-customer' } }
    )
    fireEvent.click(
      screen.getByRole('button', { name: 'Bind delivery to my account' })
    )
    await waitFor(() => expect(api.request).toHaveBeenCalledTimes(1))
    expect(
      screen.queryByRole('button', { name: 'Redeem into my wallet' })
    ).not.toBeInTheDocument()
  })
  test('bound agent deliveries have no edit/delete/shorten/cancel control, even when sales are frozen', async () => {
    vi.mocked(api.get).mockResolvedValue(ok(page([delivery])))
    await mount(<Deliveries salesEnabled={false} deliveryEnabled />)
    expect(
      await screen.findByText('Customer rights protected')
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Review cancellation' })
    ).not.toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Create customer delivery' })
    ).toBeDisabled()
  })
})
describe('procurement and financial controls', () => {
  test('quotes show original currency and a cancelled quote never creates an order', async () => {
    vi.mocked(api.request).mockResolvedValue(
      ok({
        id: 8,
        quota: 500000,
        paid_minor: '8800',
        currency: 'USD',
        currency_exponent: 2,
        policy_version: 7,
        expires_at: Math.floor(Date.now() / 1000) + 60,
      })
    )
    await mount(<PurchaseCreate onClose={vi.fn()} />)
    fireEvent.change(
      await screen.findByRole('textbox', { name: 'Procurement quota units' }),
      { target: { value: '500000' } }
    )
    fireEvent.click(screen.getByRole('button', { name: 'Continue' }))
    await screen.findByText(/USD.*88\.00/)
    expect(api.request).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    await screen.findByRole('textbox', { name: 'Procurement quota units' })
    expect(api.request).toHaveBeenCalledTimes(1)
  })
  test('expired quote obtains a new quote identity, not an expired idempotency replay', async () => {
    vi.mocked(api.request).mockResolvedValue(
      ok({
        id: 8,
        quota: 500000,
        paid_minor: '8800',
        currency: 'USD',
        currency_exponent: 2,
        policy_version: 7,
        expires_at: 1,
      })
    )
    await mount(<PurchaseCreate onClose={vi.fn()} />)
    for (let attempt = 0; attempt < 2; attempt++) {
      fireEvent.change(
        await screen.findByRole('textbox', { name: 'Procurement quota units' }),
        { target: { value: '500000' } }
      )
      fireEvent.click(screen.getByRole('button', { name: 'Continue' }))
      fireEvent.click(
        await screen.findByRole('button', { name: 'Create procurement order' })
      )
    }
    await waitFor(() => expect(api.request).toHaveBeenCalledTimes(2))
    const [first, second] = vi.mocked(api.request).mock.calls
    expect((first[0].data as { request_id: string }).request_id).not.toBe(
      (second[0].data as { request_id: string }).request_id
    )
    expect(first[0].url).toBe('/api/agent-platform/purchases/quotes')
    expect(second[0].url).toBe('/api/agent-platform/purchases/quotes')
  })
  test('buyer cannot verify their own receipt and original verifier cannot perform final review', async () => {
    vi.mocked(api.get).mockResolvedValue(
      ok(
        page([order, { ...order, id: 11, agent_user_id: 44, verified_by: 42 }])
      )
    )
    await mount(<Purchases finance />)
    expect(
      await screen.findByText('Another operator must verify your payment')
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Independent review' })
    ).toBeDisabled()
  })
  test('an unknown refund exposes reconciliation only and never a new submission', async () => {
    vi.mocked(api.get).mockResolvedValue(ok(page([refund])))
    await mount(<PurchaseRefunds finance />)
    expect(
      await screen.findByRole('button', { name: 'Reconcile payment result' })
    ).toBeEnabled()
    expect(
      screen.queryByRole('button', { name: 'Record payment submission' })
    ).not.toBeInTheDocument()
    expect(screen.getByText('agent-purchase-refund:3')).toBeInTheDocument()
  })
  test('free legacy quota cannot become a cash refund or a self-use purchase', async () => {
    vi.mocked(api.get).mockResolvedValue(
      ok(
        page([
          {
            id: 1,
            source_kind: 'legacy',
            available_quota: 500000,
            original_quota: 500000,
            original_paid_minor: '0',
            currency: '',
            cash_refundable: false,
            reserved_quota: 0,
            spent_quota: 0,
            refund_frozen_quota: 0,
            refunded_quota: 0,
          },
        ])
      )
    )
    await mount(
      <FundBatches
        settlementEnabled
        salesEnabled
        refundEnabled
        selfUseEnabled
      />
    )
    expect(
      await screen.findByRole('button', { name: 'Request purchase refund' })
    ).toBeDisabled()
    expect(
      screen.getByRole('button', { name: 'Transfer for own use' })
    ).toBeDisabled()
  })
  test('page-scoped reconciliation keeps cursor and never claims whole-system success', async () => {
    vi.mocked(api.get).mockResolvedValue(
      ok({
        anomalies: [],
        checked_agent_ids: [1, 2],
        next_cursor: 2,
        checked_at: 1700000000,
      })
    )
    await mount(<FundReconciliation />)
    expect(
      await screen.findByText(/This page checked 2 agents/)
    ).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Check next accounts' }))
    await waitFor(() =>
      expect(api.get).toHaveBeenCalledWith(
        '/api/agent-platform/finance/funds/reconciliation',
        expect.objectContaining({ params: { after_agent_id: 2, limit: 100 } })
      )
    )
  })
  test('default policy displays new features disabled and ordinary users cannot publish policy', async () => {
    vi.mocked(api.get).mockResolvedValue(
      ok({ id: 0, commission_cutover_at: 0 })
    )
    await mount(<PolicyPanel finance />)
    expect(await screen.findAllByText('Disabled')).not.toHaveLength(0)
    expect(
      screen.queryByRole('button', { name: 'Publish a new policy version' })
    ).not.toBeInTheDocument()
  })
})
