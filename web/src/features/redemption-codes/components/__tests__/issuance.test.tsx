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
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { RedemptionsMutateDrawer } from '../redemptions-mutate-drawer'
import { RedemptionsProvider } from '../redemptions-provider'

const clients: QueryClient[] = []

beforeEach(() => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'admin', role: ROLE.ADMIN })
  useSystemConfigStore.getState().setConfig({
    currency: {
      displayInCurrency: true,
      customCurrencySymbol: '¤',
      customCurrencyExchangeRate: 1,
      quotaDisplayType: 'USD',
      quotaPerUnit: 500000,
      usdExchangeRate: 1,
    },
  })
})
afterEach(() => {
  clients.forEach((client) => client.clear())
  clients.length = 0
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
})

async function setup() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  clients.push(client)
  const close = vi.fn()
  render(
    <QueryClientProvider client={client}>
      <RedemptionsProvider>
        <RedemptionsMutateDrawer open onOpenChange={close} />
      </RedemptionsProvider>
    </QueryClientProvider>
  )
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeEnabled()
  )
  fireEvent.change(screen.getByRole('textbox', { name: 'Name' }), {
    target: { value: 'customer' },
  })
  return { close }
}

it('admin creates ordinary codes directly and cannot duplicate a pending submission', async () => {
  let finish!: (value: { data: unknown }) => void
  const post = vi.spyOn(api, 'post').mockImplementation(
    () =>
      new Promise((resolve) => {
        finish = resolve
      })
  )
  const { close } = await setup()
  fireEvent.change(screen.getByRole('spinbutton', { name: 'Quota (USD)' }), {
    target: { value: '1' },
  })
  fireEvent.change(screen.getByRole('spinbutton', { name: 'Quantity' }), {
    target: { value: '3' },
  })
  await userEvent.dblClick(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() => expect(post).toHaveBeenCalledOnce())
  expect(post).toHaveBeenCalledWith(
    '/api/redemption/',
    expect.objectContaining({
      name: 'customer',
      quota: 500000,
      count: 3,
      expired_time: 0,
    })
  )
  expect(screen.getByRole('button', { name: 'Saving...' })).toBeDisabled()
  expect(
    screen.queryByRole('alertdialog', { name: 'Review transaction' })
  ).not.toBeInTheDocument()
  finish({ data: { success: true, data: ['one', 'two', 'three'] } })
  await waitFor(() => expect(close).toHaveBeenCalledWith(false))
  expect(
    await screen.findByRole('dialog', { name: 'Redemption codes created' })
  ).toBeVisible()
})

it('fractional code quantity is rejected before a request', async () => {
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue({ data: { success: true, data: [] } })
  await setup()
  fireEvent.change(screen.getByRole('spinbutton', { name: 'Quantity' }), {
    target: { value: '1.5' },
  })
  const form = document.querySelector('#redemption-form')
  if (!form) throw new Error('Expected redemption form')
  fireEvent.submit(form)
  await waitFor(() =>
    expect(
      screen.getByRole('spinbutton', { name: 'Quantity' })
    ).toHaveAttribute('aria-invalid', 'true')
  )
  expect(post).not.toHaveBeenCalled()
})

it('failed creation keeps the form open and does not export codes', async () => {
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue({ data: { success: false, message: 'Create failed' } })
  const { close } = await setup()
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() => expect(post).toHaveBeenCalledOnce())
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeEnabled()
  )
  expect(close).not.toHaveBeenCalled()
  expect(
    screen.queryByRole('dialog', { name: 'Redemption codes created' })
  ).not.toBeInTheDocument()
})

it.each([false, true])(
  'manual retry after an unknown result reuses its key unless payload changes: changed=%s',
  async (changed) => {
    const post = vi
      .spyOn(api, 'post')
      .mockRejectedValueOnce(new Error('Network unavailable'))
      .mockResolvedValueOnce({ data: { success: true, data: ['issued-code'] } })
    await setup()
    await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    await waitFor(() => expect(post).toHaveBeenCalledOnce())
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Save changes' })).toBeEnabled()
    )
    const initial = post.mock.calls[0][1] as { request_id: string }
    expect(initial.request_id).toEqual(expect.any(String))
    if (changed) {
      fireEvent.change(screen.getByRole('textbox', { name: 'Name' }), {
        target: { value: 'revised batch' },
      })
    }
    await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    await waitFor(() => expect(post).toHaveBeenCalledTimes(2))
    const retried = post.mock.calls[1][1] as { request_id: string }
    if (changed) expect(retried.request_id).not.toBe(initial.request_id)
    else expect(retried).toEqual(initial)
  }
)
