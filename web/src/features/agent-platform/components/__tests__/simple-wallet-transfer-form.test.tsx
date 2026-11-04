/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'
import { useSystemConfigStore } from '@/stores/system-config-store'

import {
  useWalletTransferStore,
  walletTransferVersion,
} from '../../stores/wallet-transfer-store'
import { SimpleWalletTransferForm } from '../simple-wallet-transfer-form'

function deferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((done, fail) => {
    resolve = done
    reject = fail
  })
  return { promise, resolve, reject }
}

beforeEach(() => {
  localStorage.clear()
  useAuthStore.getState().auth.setUser({ id: 42, username: 'Agent', role: 1 })
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...useSystemConfigStore.getState().config.currency,
      quotaPerUnit: 500_000,
    },
  })
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: { paid: 5_000_000, bonus: 90_000_000, reward: 90_000_000 },
    },
  })
  let tail = Promise.resolve<unknown>(undefined)
  Object.defineProperty(navigator, 'locks', {
    configurable: true,
    value: {
      request: (_name: string, criticalSection: () => unknown) => {
        const result = tail.then(criticalSection)
        tail = result.catch(() => undefined)
        return result
      },
    },
  })
})
afterEach(() => {
  cleanup()
  localStorage.clear()
  vi.restoreAllMocks()
})
function mount(userId = 42) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const onClose = vi.fn()
  const component = (id: number) => (
    <QueryClientProvider client={client}>
      <SimpleWalletTransferForm userId={id} onClose={onClose} />
    </QueryClientProvider>
  )
  const view = render(component(userId))
  return {
    ...view,
    client,
    onClose,
    changeUser: (id: number) => {
      act(() =>
        useAuthStore.getState().auth.setUser({ id, username: 'Other', role: 1 })
      )
      view.rerender(component(id))
    },
  }
}
async function amount(value = '5.25') {
  fireEvent.change(await screen.findByRole('textbox', { name: 'Amount ($)' }), {
    target: { value },
  })
}
const store = () => useWalletTransferStore.getState()

test('success transfers exact 1:1 quota with expected identity and invalidates wallet and agent data', async () => {
  const request = vi.spyOn(api, 'request').mockResolvedValue({
    data: { success: true, data: { wallet_quota: 2_625_000 } },
  })
  const view = mount()
  view.client.setQueryData(['wallet', 'user'], { quota: 5_000_000 })
  view.client.setQueryData(['agent-platform', 42, 'simple-balance'], {
    wallet_quota: 0,
  })
  await amount()
  fireEvent.click(screen.getByRole('button', { name: 'Transfer' }))
  await waitFor(() => expect(view.onClose).toHaveBeenCalledOnce())
  expect(request).toHaveBeenCalledOnce()
  expect(request.mock.calls[0][0]).toMatchObject({
    url: '/api/agent-platform/simple/wallet-transfer',
    data: {
      quota: 2_625_000,
      expected_user_id: 42,
      request_id: expect.any(String),
    },
  })
  expect(view.client.getQueryState(['wallet', 'user'])?.isInvalidated).toBe(
    true
  )
  expect(
    view.client.getQueryState(['agent-platform', 42, 'simple-balance'])
      ?.isInvalidated
  ).toBe(true)
  expect(store().read(42)?.status).toBe('completed')
})
test.each(['0', '-1', '10.01', '0.001', 'NaN'])(
  'invalid amount %s cannot spend bonus or reward',
  async (value) => {
    const request = vi.spyOn(api, 'request')
    mount()
    await amount(value)
    fireEvent.click(screen.getByRole('button', { name: 'Transfer' }))
    expect(
      await screen.findByText(
        'Enter a valid amount within your paid wallet balance.'
      )
    ).toBeVisible()
    expect(request).not.toHaveBeenCalled()
  }
)
test('cancel before submission creates no transfer', async () => {
  const request = vi.spyOn(api, 'request')
  const view = mount()
  await amount()
  fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(view.onClose).toHaveBeenCalledOnce()
  expect(request).not.toHaveBeenCalled()
  expect(store().read(42)).toBeNull()
})
test('double clicks and Escape during submission cannot repeat or dismiss the transfer', async () => {
  const response = deferred<{ data: { success: boolean; data: object } }>()
  const request = vi.spyOn(api, 'request').mockReturnValue(response.promise)
  const view = mount()
  await amount()
  const submit = screen.getByRole('button', { name: 'Transfer' })
  fireEvent.click(submit)
  fireEvent.click(submit)
  await waitFor(() => expect(request).toHaveBeenCalledOnce())
  expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled()
  fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' })
  expect(view.onClose).not.toHaveBeenCalled()
  response.resolve({ data: { success: true, data: {} } })
  await waitFor(() => expect(view.onClose).toHaveBeenCalledOnce())
})
test('lost response reopens immutable original transfer even if wallet is empty', async () => {
  const request = vi
    .spyOn(api, 'request')
    .mockRejectedValueOnce(new Error('Lost response'))
    .mockResolvedValue({ data: { success: true, data: {} } })
  const first = mount()
  await amount()
  fireEvent.click(screen.getByRole('button', { name: 'Transfer' }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Retry transfer' })).toBeEnabled()
  )
  first.unmount()
  vi.mocked(api.get).mockResolvedValue({
    data: { success: true, data: { paid: 0 } },
  })
  const second = mount()
  expect(
    await screen.findByRole('textbox', { name: 'Amount ($)' })
  ).toHaveValue('5.25')
  expect(screen.getByRole('textbox', { name: 'Amount ($)' })).toHaveAttribute(
    'readonly'
  )
  fireEvent.click(screen.getByRole('button', { name: 'Retry transfer' }))
  await waitFor(() => expect(second.onClose).toHaveBeenCalledOnce())
  expect(request.mock.calls[1][0]?.data).toEqual(request.mock.calls[0][0]?.data)
})
test.each([
  [400, 'invalid simplified agent request'],
  [409, 'insufficient agent balance or credit'],
  [409, 'Wallet quota limit exceeded'],
])(
  'definite rejection %s/%s permits correction',
  async (status, message_key) => {
    const request = vi.spyOn(api, 'request').mockRejectedValue({
      response: { status, data: { success: false, message_key } },
    })
    mount()
    await amount()
    fireEvent.click(screen.getByRole('button', { name: 'Transfer' }))
    await waitFor(() => expect(request).toHaveBeenCalledOnce())
    await waitFor(() =>
      expect(
        screen.getByRole('textbox', { name: 'Amount ($)' })
      ).not.toHaveAttribute('readonly')
    )
    expect(store().read(42)?.status).toBe('rejected')
  }
)
test.each([
  [403, 'invalid simplified agent request'],
  [409, 'operation conflict'],
  [500, 'Wallet quota limit exceeded'],
])(
  'uncertain response %s/%s preserves pending identity',
  async (status, message_key) => {
    vi.spyOn(api, 'request').mockRejectedValue({
      response: { status, data: { success: false, message_key } },
    })
    mount()
    await amount()
    fireEvent.click(screen.getByRole('button', { name: 'Transfer' }))
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Retry transfer' })
      ).toBeEnabled()
    )
    expect(store().read(42)?.status).toBe('pending')
  }
)
test('stale blank page adopts another pending amount only for review without posting it', async () => {
  const request = vi.spyOn(api, 'request')
  mount()
  await amount('2')
  const pending = await store().begin(42, null, 500_000, '1')
  fireEvent.click(screen.getByRole('button', { name: 'Transfer' }))
  await waitFor(() =>
    expect(screen.getByRole('textbox', { name: 'Amount ($)' })).toHaveValue('1')
  )
  expect(request).not.toHaveBeenCalled()
  expect(store().read(42)).toEqual(pending)
})
test('a stale retry completed elsewhere does not POST with a new identity', async () => {
  const pending = await store().begin(42, null, 500_000, '1')
  const request = vi.spyOn(api, 'request')
  mount()
  await screen.findByRole('button', { name: 'Retry transfer' })
  await store().finish(42, pending.request_id, 'completed')
  fireEvent.click(screen.getByRole('button', { name: 'Retry transfer' }))
  await screen.findByRole('button', { name: 'Transfer' })
  expect(request).not.toHaveBeenCalled()
})
test('late success after switching users cannot close or overwrite the new user form', async () => {
  const response = deferred<{ data: { success: boolean; data: object } }>()
  const request = vi.spyOn(api, 'request').mockReturnValue(response.promise)
  const view = mount()
  await amount()
  fireEvent.click(screen.getByRole('button', { name: 'Transfer' }))
  await waitFor(() => expect(request).toHaveBeenCalledOnce())
  view.changeUser(43)
  await amount('3')
  response.resolve({
    data: { success: true, data: { wallet_quota: 2_625_000 } },
  })
  await waitFor(() => expect(store().read(42)?.status).toBe('completed'))
  expect(view.onClose).not.toHaveBeenCalled()
  expect(screen.getByRole('textbox', { name: 'Amount ($)' })).toHaveValue('3')
  expect(store().read(43)).toBeNull()
})
test('user switch while waiting for a lock prevents sending with the new session', async () => {
  const lock = deferred<void>()
  const request = vi.spyOn(api, 'request')
  vi.spyOn(navigator.locks, 'request').mockImplementation(
    async (_name, callback) => {
      await lock.promise
      return (callback as () => unknown)()
    }
  )
  const view = mount()
  await amount()
  fireEvent.click(screen.getByRole('button', { name: 'Transfer' }))
  await waitFor(() => expect(navigator.locks.request).toHaveBeenCalledOnce())
  view.changeUser(43)
  await act(async () => {
    lock.resolve()
    await lock.promise
  })
  expect(request).not.toHaveBeenCalled()
  expect(view.onClose).not.toHaveBeenCalled()
})

test.each(['success', 'rejection'])(
  'late %s cannot delete or misdisplay the newer cross-page intent',
  async (outcome) => {
    const response = deferred<{ data: { success: boolean; data: object } }>()
    const request = vi.spyOn(api, 'request').mockReturnValue(response.promise)
    const view = mount()
    await amount('1')
    fireEvent.click(screen.getByRole('button', { name: 'Transfer' }))
    await waitFor(() => expect(request).toHaveBeenCalledOnce())
    const first = store().read(42)
    if (!first) throw new Error('Expected a pending transfer')
    await store().finish(42, first.request_id, 'completed')
    const second = await store().begin(
      42,
      walletTransferVersion(store().read(42)),
      1_000_000,
      '2'
    )
    if (outcome === 'success') {
      response.resolve({ data: { success: true, data: {} } })
    } else {
      response.reject({
        response: {
          status: 409,
          data: {
            success: false,
            message_key: 'insufficient agent balance or credit',
          },
        },
      })
    }
    await waitFor(() =>
      expect(screen.getByRole('textbox', { name: 'Amount ($)' })).toHaveValue(
        '2'
      )
    )
    expect(store().read(42)).toEqual(second)
    expect(view.onClose).not.toHaveBeenCalled()
    expect(request).toHaveBeenCalledOnce()
  }
)

test('storage write failure cannot send a transfer without durable recovery identity', async () => {
  const request = vi.spyOn(api, 'request')
  const write = vi.spyOn(localStorage, 'setItem').mockImplementation(() => {
    throw new Error('Storage unavailable')
  })
  const view = mount()
  await amount()
  fireEvent.click(screen.getByRole('button', { name: 'Transfer' }))
  await waitFor(() => expect(write).toHaveBeenCalled())
  expect(request).not.toHaveBeenCalled()
  expect(view.onClose).not.toHaveBeenCalled()
})

test('unclassified business failure must not close the dialog or release its identity', async () => {
  vi.spyOn(api, 'request').mockResolvedValue({
    data: { success: false, message: 'Operation status unavailable' },
  })
  const view = mount()
  await amount()
  fireEvent.click(screen.getByRole('button', { name: 'Transfer' }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Retry transfer' })).toBeEnabled()
  )
  expect(store().read(42)?.status).toBe('pending')
  expect(view.onClose).not.toHaveBeenCalled()
})
