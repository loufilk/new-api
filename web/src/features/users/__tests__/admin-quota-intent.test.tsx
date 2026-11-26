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
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { useState } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'

import { UserQuotaDialog } from '../components/user-quota-dialog'
import { quotaIntentKey } from '../lib/quota-intent'
import type { ManageUserQuotaPayload, QuotaAdjustMode } from '../types'

const uuidPattern =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
let previousCurrency = useSystemConfigStore.getState().config.currency

const retryName = /Retry the same request|intelligence\.retrySame/i
const scope = { actorId: 42, userId: 17 }

function actor(id: number) {
  useAuthStore.getState().auth.setUser({ id, username: 'synthetic', role: 10 })
}
beforeEach(() => {
  localStorage.clear()
  actor(42)
  // Model exclusive ifAvailable semantics without involving any real ledger.
  const held = new Set<string>()
  Object.defineProperty(navigator, 'locks', {
    configurable: true,
    value: {
      request: async (
        name: string,
        _options: unknown,
        fn: (lock: unknown) => Promise<void>
      ) => {
        if (held.has(name)) return fn(null)
        held.add(name)
        try {
          await fn({ name })
        } finally {
          held.delete(name)
        }
      },
    },
  })
  previousCurrency = useSystemConfigStore.getState().config.currency
  useSystemConfigStore
    .getState()
    .setConfig({ currency: DEFAULT_CURRENCY_CONFIG })
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  localStorage.clear()
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
  useSystemConfigStore.getState().setConfig({ currency: previousCurrency })
})

function Harness({
  userId = 17,
  currentQuota = 3_000_000,
  onSuccess = () => undefined,
}: {
  userId?: number
  currentQuota?: number
  onSuccess?: () => void
}) {
  const [open, setOpen] = useState(true)
  return (
    <>
      <button type='button' onClick={() => setOpen(true)}>
        Open quota
      </button>
      <UserQuotaDialog
        open={open}
        onOpenChange={setOpen}
        userId={userId}
        currentQuota={currentQuota}
        onSuccess={onSuccess}
      />
    </>
  )
}

function fill(amount = '1') {
  const input = screen.getByRole('spinbutton')
  fireEvent.change(input, { target: { value: amount } })
  return input
}

async function submitRejected() {
  const confirm = screen.getByRole('button', { name: 'Confirm' })
  fireEvent.click(confirm)
  await waitFor(() =>
    expect(screen.getByRole('button', { name: retryName })).toBeEnabled()
  )
}

function capturedWireBody(body: unknown): ManageUserQuotaPayload {
  // Preserve HTTP JSON semantics: Infinity must become null, not survive cloning.
  // oxlint-disable-next-line unicorn/prefer-structured-clone
  return JSON.parse(JSON.stringify(body)) as ManageUserQuotaPayload
}

describe('admin quota intent at the existing HTTP boundary', () => {
  it.each(['1e308', '18014398510'])(
    'does not serialize a non-representable override amount (%s) into a wire mutation',
    async (amount) => {
      const wire: ManageUserQuotaPayload[] = []
      vi.spyOn(api, 'post').mockImplementation(async (_url, body) => {
        wire.push(capturedWireBody(body))
        return {
          data: { success: false, message: 'Owned invalid-value fixture' },
        }
      })
      render(<Harness />)
      fireEvent.click(screen.getByRole('button', { name: 'Override' }))
      const input = fill(amount)
      expect(input).toHaveValue(Number(amount))
      await act(async () => {
        fireEvent.click(screen.getByRole('button', { name: 'Confirm' }))
      })
      expect(wire).toEqual([])
    }
  )

  it('preserves an explicit zero override as a valid existing operation', async () => {
    const post = vi.spyOn(api, 'post').mockRejectedValue(new Error('offline'))
    render(<Harness />)
    fireEvent.click(screen.getByRole('button', { name: 'Override' }))
    fill('0')
    await submitRejected()
    expect(capturedWireBody(post.mock.calls[0][1])).toMatchObject({
      mode: 'override',
      value: 0,
      request_id: expect.stringMatching(uuidPattern),
    })
  })

  it('sends the original admin endpoint, quota units and UUID contract', async () => {
    const post = vi.spyOn(api, 'post').mockRejectedValue(new Error('offline'))
    render(<Harness />)
    fill()
    await submitRejected()
    expect(post).toHaveBeenCalledTimes(1)
    expect(post.mock.calls[0][0]).toBe('/api/user/manage')
    expect(post.mock.calls[0][2]).toEqual({ singleUseAuthorization: true })
    expect(capturedWireBody(post.mock.calls[0][1])).toEqual({
      id: 17,
      action: 'add_quota',
      mode: 'add',
      value: 500_000,
      request_id: expect.stringMatching(uuidPattern),
    })
  })

  it.each(['double Enter', 'click then Enter'])(
    'allows only one in-flight HTTP attempt for %s',
    async (trigger) => {
      let release!: () => void
      const pending = new Promise<{ data: { success: boolean } }>((resolve) => {
        release = () => resolve({ data: { success: true } })
      })
      const post = vi.spyOn(api, 'post').mockReturnValue(pending)
      render(<Harness />)
      const input = fill()
      const confirm = screen.getByRole('button', { name: 'Confirm' })
      act(() => {
        if (trigger === 'double Enter') {
          fireEvent.keyDown(input, { key: 'Enter' })
        } else {
          fireEvent.click(confirm)
        }
        fireEvent.keyDown(input, { key: 'Enter' })
      })
      try {
        expect(post).toHaveBeenCalledTimes(1)
      } finally {
        await act(async () => release())
      }
    }
  )

  it.each<QuotaAdjustMode>(['add', 'subtract', 'override'])(
    'retains the %s intent key after an unknown ACK, even if displayed balance changes',
    async (mode) => {
      const wire: ManageUserQuotaPayload[] = []
      // Owned contract mock only: records an intent, then loses the first ACK.
      // This is not evidence of real database/ledger exactly-once behavior.
      const recordedIntents = new Set<string>()
      vi.spyOn(api, 'post').mockImplementation(async (_url, body) => {
        const request = capturedWireBody(body)
        wire.push(request)
        recordedIntents.add(request.request_id)
        if (wire.length === 1) {
          throw new Error('ACK lost after recording intent')
        }
        return { data: { success: true } }
      })
      const onSuccess = vi.fn()
      const view = render(<Harness onSuccess={onSuccess} />)
      if (mode !== 'add') {
        fireEvent.click(
          screen.getByRole('button', {
            name: mode === 'subtract' ? 'Subtract' : 'Override',
          })
        )
      }
      fill()
      await submitRejected()
      expect(wire).toHaveLength(1)
      expect(wire[0].request_id).toMatch(uuidPattern)
      view.unmount()
      render(<Harness currentQuota={4_000_000} onSuccess={onSuccess} />)
      expect(screen.getByText(wire[0].request_id)).toBeVisible()
      expect(screen.queryByRole('spinbutton')).toBeNull()
      expect(wire).toHaveLength(1)
      fireEvent.click(screen.getByRole('button', { name: retryName }))
      await waitFor(() => expect(onSuccess).toHaveBeenCalledTimes(1))
      expect(wire).toHaveLength(2)
      expect(wire[1]).toEqual(wire[0])
      expect(recordedIntents.size).toBe(1)
    }
  )

  it('cannot edit or mint another operation after an unknown result, including after close/reopen', async () => {
    const post = vi.spyOn(api, 'post').mockRejectedValue(new Error('lost ACK'))
    render(<Harness />)
    fill()
    await submitRejected()
    const first = capturedWireBody(post.mock.calls[0][1])
    expect(
      JSON.parse(localStorage.getItem(quotaIntentKey(scope)) ?? 'null')
    ).toEqual({
      ...scope,
      mode: 'add',
      value: 500_000,
      request_id: first.request_id,
      status: 'pending',
    })
    fireEvent.click(screen.getByText('Close', { selector: 'button' }))
    fireEvent.click(screen.getByRole('button', { name: 'Open quota' }))
    expect(screen.getByText(first.request_id)).toBeVisible()
    expect(screen.queryByRole('spinbutton')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Subtract' })).toBeNull()
    expect(post).toHaveBeenCalledTimes(1)
  })

  it('never exposes or replays the tuple for a different actor or a different target', async () => {
    const post = vi.spyOn(api, 'post').mockRejectedValue(new Error('network'))
    const view = render(<Harness />)
    fill()
    await submitRejected()
    const first = capturedWireBody(post.mock.calls[0][1])
    act(() => actor(43))
    expect(screen.queryByText(first.request_id)).toBeNull()
    expect(screen.getByRole('spinbutton')).toHaveValue(null)
    act(() => actor(42))
    expect(screen.getByText(first.request_id)).toBeVisible()
    view.rerender(<Harness userId={18} />)
    expect(screen.queryByText(first.request_id)).toBeNull()
    expect(screen.getByRole('spinbutton')).toHaveValue(null)
    expect(post).toHaveBeenCalledTimes(1)
  })

  it('does not close the new actor form when the old actor receives a late success', async () => {
    let release!: () => void
    vi.spyOn(api, 'post').mockImplementation(
      () =>
        new Promise((resolve) => {
          release = () => resolve({ data: { success: true } })
        })
    )
    const onSuccess = vi.fn()
    render(<Harness onSuccess={onSuccess} />)
    fill()
    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }))
    act(() => actor(55))
    await act(async () => release())
    expect(onSuccess).not.toHaveBeenCalled()
    expect(screen.getByRole('spinbutton')).toBeVisible()
    expect(
      localStorage.getItem(quotaIntentKey({ actorId: 55, userId: 17 }))
    ).toBeNull()
  })

  it('keeps an unresolved adjustment on a later rejection but unlocks a first confirmed rejection', async () => {
    const rejection = {
      data: { success: false, message: 'insufficient balance' },
    }
    const post = vi
      .spyOn(api, 'post')
      .mockResolvedValueOnce(rejection)
      .mockRejectedValueOnce(new Error('unknown'))
      .mockResolvedValueOnce(rejection)
    render(<Harness />)
    fill()
    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }))
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Confirm' })).toBeEnabled()
    )
    fill('2')
    await submitRejected()
    fireEvent.click(screen.getByRole('button', { name: retryName }))
    await waitFor(() =>
      expect(screen.getByRole('button', { name: retryName })).toBeEnabled()
    )
    const bodies = post.mock.calls.map((call) => capturedWireBody(call[1]))
    expect(bodies[0].request_id).not.toBe(bodies[1].request_id)
    expect(bodies[1]).toEqual(bodies[2])
  })

  it('requires review of a changed scope record before a stale tab can dispatch', async () => {
    const post = vi
      .spyOn(api, 'post')
      .mockResolvedValue({ data: { success: true } })
    render(<Harness />)
    fill('17')
    const other = {
      ...scope,
      request_id: crypto.randomUUID(),
      mode: 'subtract',
      value: 71,
      status: 'pending',
    }
    localStorage.setItem(quotaIntentKey(scope), JSON.stringify(other))
    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }))
    expect(await screen.findByText(other.request_id)).toBeVisible()
    expect(post).not.toHaveBeenCalled()
    // The other tab finished before this operator clicked retry; do not replay
    // its operation or implicitly start a fresh one.
    localStorage.setItem(
      quotaIntentKey(scope),
      JSON.stringify({
        ...scope,
        request_id: other.request_id,
        status: 'closed',
      })
    )
    fireEvent.click(screen.getByRole('button', { name: retryName }))
    await screen.findByRole('spinbutton')
    expect(post).not.toHaveBeenCalled()
  })

  it('sends nothing when storage cannot journal the request or is corrupt', async () => {
    const post = vi
      .spyOn(api, 'post')
      .mockRejectedValue(new Error('Unexpected call'))
    localStorage.setItem(quotaIntentKey(scope), '{}')
    render(<Harness />)
    expect(screen.getByRole('button', { name: 'Confirm' })).toBeDisabled()
    cleanup()
    localStorage.clear()
    const savedStorage = localStorage
    vi.stubGlobal('localStorage', {
      getItem: (key: string) => savedStorage.getItem(key),
      setItem: () => {
        throw new Error('full')
      },
    })
    render(<Harness />)
    fill()
    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }))
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Confirm' })).toBeDisabled()
    )
    expect(post).not.toHaveBeenCalled()
  })
  it('does not reuse a successful intent for the next explicit operation', async () => {
    const post = vi
      .spyOn(api, 'post')
      .mockResolvedValue({ data: { success: true } })
    const onSuccess = vi.fn()
    render(<Harness onSuccess={onSuccess} />)
    fill()
    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }))
    await waitFor(() => expect(onSuccess).toHaveBeenCalledTimes(1))
    fireEvent.click(await screen.findByRole('button', { name: 'Open quota' }))
    fill()
    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }))
    await waitFor(() => expect(onSuccess).toHaveBeenCalledTimes(2))
    const first = capturedWireBody(post.mock.calls[0][1])
    const second = capturedWireBody(post.mock.calls[1][1])
    expect(second.value).toBe(first.value)
    expect(second.request_id).toMatch(uuidPattern)
    expect(second.request_id).not.toBe(first.request_id)
    const record = JSON.parse(
      localStorage.getItem(quotaIntentKey(scope)) ?? 'null'
    )
    expect(record).toEqual({
      ...scope,
      status: 'closed',
      request_id: second.request_id,
    })
  })
})
