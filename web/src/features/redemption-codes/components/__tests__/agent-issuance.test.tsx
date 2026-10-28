import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { RedemptionsMutateDrawer } from '../redemptions-mutate-drawer'
import { RedemptionsProvider } from '../redemptions-provider'

const clients: QueryClient[] = []
const availableQuota = 1500000

beforeEach(() => {
  useAuthStore.getState().auth.setUser({ id: 7, username: 'agent', role: ROLE.AGENT })
  useSystemConfigStore.getState().setConfig({ currency: { quotaDisplayType: 'USD', quotaPerUnit: 500000, usdExchangeRate: 1 } })
  vi.spyOn(api, 'get').mockResolvedValue({ data: { success: true, data: { items: [{ agent_user_id: 7, available_quota: availableQuota, reserved_quota: 0, spent_quota: 0, allocated_quota: availableQuota }], total: 1 } } })
})
afterEach(() => { clients.forEach((client) => client.clear()); clients.length = 0; useAuthStore.getState().auth.reset(); vi.restoreAllMocks() })

async function setup() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  clients.push(client)
  const close = vi.fn()
  render(<QueryClientProvider client={client}><RedemptionsProvider><RedemptionsMutateDrawer open onOpenChange={close} /></RedemptionsProvider></QueryClientProvider>)
  await waitFor(() => expect(screen.getByRole('button', { name: 'Save changes' })).toBeEnabled())
  fireEvent.change(screen.getByRole('textbox', { name: 'Name' }), { target: { value: 'customer' } })
  return { close, user: userEvent.setup() }
}

function enterBatch(amount: string, count: string) {
  fireEvent.change(screen.getByRole('spinbutton', { name: 'Quota (USD)' }), { target: { value: amount } })
  fireEvent.change(screen.getByRole('spinbutton', { name: 'Quantity' }), { target: { value: count } })
}

it('rejects a batch exceeding available quota without posting any codes', async () => {
  const post = vi.spyOn(api, 'post')
  await setup()
  enterBatch('1.0001', '3')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(await screen.findByText('Insufficient available agent quota')).toBeInTheDocument()
  expect(post).not.toHaveBeenCalled()
})

it('rejects fractional batch quantities rather than truncating them', async () => {
  const post = vi.spyOn(api, 'post')
  await setup()
  enterBatch('1', '1.5')
  fireEvent.submit(document.querySelector('#redemption-form')!)
  await waitFor(() => expect(screen.getByRole('spinbutton', { name: 'Quantity' })).toHaveAttribute('aria-invalid', 'true'))
  expect(post).not.toHaveBeenCalled()
})

it('reserves an exact-budget batch once and blocks cancel and repeat clicks while pending', async () => {
  let resolve!: (value: unknown) => void
  const post = vi.spyOn(api, 'post').mockImplementation(() => new Promise((done) => { resolve = done }))
  const { close, user } = await setup()
  enterBatch('1', '3')
  await user.dblClick(screen.getByRole('button', { name: 'Save changes' }))
  expect(screen.getByRole('button', { name: 'Saving...' })).toBeDisabled()
  const closeButtons = screen.getAllByRole('button', { name: 'Close' })
  for (const button of closeButtons) { if (!button.hasAttribute('disabled')) await user.click(button) }
  await user.keyboard('{Escape}')
  expect(close).not.toHaveBeenCalled()
  expect(post).toHaveBeenCalledTimes(1)
  expect(post).toHaveBeenCalledWith('/api/redemption/', expect.objectContaining({ quota: 500000, count: 3, request_id: expect.any(String) }))
  resolve({ data: { success: true, data: ['one', 'two', 'three'] } })
  await waitFor(() => expect(close).toHaveBeenCalledWith(false))
})

it('keeps failed issuance open and preserves the same idempotency key on retry', async () => {
  const post = vi.spyOn(api, 'post').mockResolvedValueOnce({ data: { success: false, message: 'Insufficient available agent redemption quota' } }).mockResolvedValueOnce({ data: { success: true, data: ['code'] } })
  const { close } = await setup()
  enterBatch('1', '1')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() => expect(screen.getByRole('button', { name: 'Save changes' })).toBeEnabled())
  expect(close).not.toHaveBeenCalled()
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() => expect(close).toHaveBeenCalledWith(false))
  expect(post.mock.calls[0]?.[1]).toEqual(post.mock.calls[1]?.[1])
})

it('closing an unsubmitted batch never creates redemption codes', async () => {
  const post = vi.spyOn(api, 'post')
  const { close, user } = await setup()
  await user.keyboard('{Escape}')
  expect(close).toHaveBeenCalledWith(false)
  expect(post).not.toHaveBeenCalled()
})
