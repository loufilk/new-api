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
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { useState } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'

import { UserQuotaDialog } from '../components/user-quota-dialog'
import type { ManageUserQuotaPayload, QuotaAdjustMode } from '../types'

const uuidPattern =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
let previousCurrency = useSystemConfigStore.getState().config.currency

beforeEach(() => {
  previousCurrency = useSystemConfigStore.getState().config.currency
  useSystemConfigStore
    .getState()
    .setConfig({ currency: DEFAULT_CURRENCY_CONFIG })
})

afterEach(() => {
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
  await waitFor(() => expect(confirm).toBeEnabled())
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
      view.rerender(<Harness currentQuota={4_000_000} onSuccess={onSuccess} />)
      fireEvent.click(screen.getByRole('button', { name: 'Confirm' }))
      await waitFor(() => expect(onSuccess).toHaveBeenCalledTimes(1))
      expect(wire).toHaveLength(2)
      expect(wire[1]).toEqual(wire[0])
      expect(recordedIntents.size).toBe(1)
    }
  )

  it('retains the unresolved intent when amount formatting leaves the backend value unchanged', async () => {
    const post = vi
      .spyOn(api, 'post')
      .mockRejectedValue(new Error('ACK unknown'))
    render(<Harness />)
    fill()
    await submitRejected()
    const input = fill('1.0') as HTMLInputElement
    expect(input.value).toBe('1.0')
    await submitRejected()
    expect(post).toHaveBeenCalledTimes(2)
    const first = capturedWireBody(post.mock.calls[0][1])
    const second = capturedWireBody(post.mock.calls[1][1])
    expect(first.request_id).toMatch(uuidPattern)
    expect(second.value).toBe(first.value)
    expect(second).toEqual(first)
  })

  it('starts a new intent when the amount changes, including change then restore', async () => {
    const post = vi
      .spyOn(api, 'post')
      .mockRejectedValue(new Error('ACK unknown'))
    render(<Harness />)
    fill()
    await submitRejected()
    fill('2')
    fill('1')
    await submitRejected()
    const first = capturedWireBody(post.mock.calls[0][1])
    const second = capturedWireBody(post.mock.calls[1][1])
    expect(first.request_id).toMatch(uuidPattern)
    expect(second.value).toBe(first.value)
    expect(second.request_id).not.toBe(first.request_id)
  })

  it('starts a new intent when the mode changes', async () => {
    const post = vi
      .spyOn(api, 'post')
      .mockRejectedValue(new Error('ACK unknown'))
    render(<Harness />)
    fill()
    await submitRejected()
    fireEvent.click(screen.getByRole('button', { name: 'Subtract' }))
    fill()
    await submitRejected()
    const first = capturedWireBody(post.mock.calls[0][1])
    const second = capturedWireBody(post.mock.calls[1][1])
    expect(second.mode).toBe('subtract')
    expect(second.request_id).toMatch(uuidPattern)
    expect(second.request_id).not.toBe(first.request_id)
  })

  it('never reuses an unresolved intent for a different target user', async () => {
    const post = vi
      .spyOn(api, 'post')
      .mockRejectedValue(new Error('ACK unknown'))
    const view = render(<Harness />)
    fill()
    await submitRejected()
    view.rerender(<Harness userId={18} />)
    await submitRejected()
    const first = capturedWireBody(post.mock.calls[0][1])
    const second = capturedWireBody(post.mock.calls[1][1])
    expect(second.id).toBe(18)
    expect(second.request_id).toMatch(uuidPattern)
    expect(second.request_id).not.toBe(first.request_id)
  })

  it('starts a new explicit operation after cancellation and reopening', async () => {
    const post = vi
      .spyOn(api, 'post')
      .mockRejectedValue(new Error('ACK unknown'))
    render(<Harness />)
    fill()
    await submitRejected()
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Open quota' }))
    fill()
    await submitRejected()
    expect(capturedWireBody(post.mock.calls[1][1]).request_id).not.toBe(
      capturedWireBody(post.mock.calls[0][1]).request_id
    )
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
  })
})
