import { act, renderHook, waitFor } from '@testing-library/react'
import { beforeEach, expect, test, vi } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'
import { handleServerError } from '@/lib/handle-server-error'
import { usePayment } from './use-payment'
import { calculateAmount, getEpayIntent, requestPayment } from '../api'
import { submitPaymentForm } from '../lib'
import { useEpayIntent } from './use-epay-intent'

vi.mock('../api', async (original) => ({
  ...(await original<typeof import('../api')>()),
  calculateAmount: vi.fn(),
  getEpayIntent: vi.fn(),
  requestPayment: vi.fn(),
}))
vi.mock('../lib', async (original) => ({
  ...(await original<typeof import('../lib')>()),
  submitPaymentForm: vi.fn(),
}))
vi.mock('@/lib/handle-server-error', () => ({ handleServerError: vi.fn() }))
const gateway = 'a'.repeat(32)
const trade_no = 'EI' + 'A'.repeat(26)
const pending = {
  success: true,
  trade_no,
  payment_minor: 7300,
  payment_method: 'alipay',
  order_status: 'pending' as const,
}
const accepted = {
  ...pending,
  url: 'https://pay.example.invalid',
  data: { out_trade_no: trade_no },
}
beforeEach(() => {
  vi.clearAllMocks()
  sessionStorage.clear()
  useAuthStore
    .getState()
    .auth.setUser({ id: 7, username: 'fixture', role: 1 } as NonNullable<
      ReturnType<typeof useAuthStore.getState>['auth']['user']
    >)
  vi.mocked(getEpayIntent).mockResolvedValue(pending)
  vi.mocked(requestPayment).mockResolvedValue(accepted)
})
test('lost acknowledgement, double click, refresh and explicit continuation use one id; lookup never posts a checkout', async () => {
  let reject!: (e: Error) => void
  vi.mocked(requestPayment).mockImplementationOnce(
    () =>
      new Promise((_r, j) => {
        reject = j
      })
  )
  const first = renderHook(useEpayIntent)
  let attempt!: Promise<boolean>
  act(() => {
    attempt = first.result.current.pay(10, 'alipay', 7300, gateway)
  })
  expect(await first.result.current.pay(10, 'alipay', 7300, gateway)).toBe(
    false
  )
  const id = vi.mocked(requestPayment).mock.calls[0][0].intent_id
  await act(async () => {
    reject(Error('lost ACK'))
    expect(await attempt).toBe(false)
  })
  expect(first.result.current.recovery?.tradeNo).toBe(trade_no)
  first.unmount()
  const restored = renderHook(useEpayIntent)
  await waitFor(() =>
    expect(restored.result.current.recovery?.status).toBe('pending')
  )
  expect(requestPayment).toHaveBeenCalledTimes(1)
  expect(submitPaymentForm).not.toHaveBeenCalled()
  let ok = false
  await act(async () => {
    ok = await restored.result.current.pay(10, 'alipay', 7300, gateway)
  })
  expect(ok).toBe(true)
  expect(vi.mocked(requestPayment).mock.calls[1][0].intent_id).toBe(id)
  expect(submitPaymentForm).toHaveBeenCalledTimes(1)
  expect(restored.result.current.recovery?.tradeNo).toBe(trade_no)
  restored.unmount()
  const afterHandoff = renderHook(useEpayIntent)
  await waitFor(() =>
    expect(afterHandoff.result.current.recovery?.tradeNo).toBe(trade_no)
  )
  await act(async () => {
    expect(
      await afterHandoff.result.current.pay(10, 'alipay', 7300, gateway)
    ).toBe(false)
  })
  expect(requestPayment).toHaveBeenCalledTimes(2)
  expect(submitPaymentForm).toHaveBeenCalledTimes(1)
})
test('an unmounted request does not launch payment; terminal status never reopens checkout', async () => {
  let resolve!: (r: typeof accepted) => void
  vi.mocked(requestPayment).mockImplementationOnce(
    () =>
      new Promise((r) => {
        resolve = r
      })
  )
  const first = renderHook(useEpayIntent)
  let flight!: Promise<boolean>
  act(() => {
    flight = first.result.current.pay(10, 'alipay', 7300, gateway)
  })
  first.unmount()
  resolve(accepted)
  expect(await flight).toBe(false)
  vi.mocked(getEpayIntent).mockResolvedValue({
    ...pending,
    order_status: 'success',
  })
  const next = renderHook(useEpayIntent)
  await waitFor(() =>
    expect(next.result.current.recovery?.status).toBe('success')
  )
  vi.mocked(requestPayment).mockResolvedValue({
    ...pending,
    order_status: 'success',
  })
  await act(async () => {
    expect(await next.result.current.pay(10, 'alipay', 7300, gateway)).toBe(
      false
    )
  })
  expect(submitPaymentForm).not.toHaveBeenCalled()
})
test('untyped gateway 404 cannot start a different checkout; authoritative absence may update terms but not the id', async () => {
  vi.mocked(requestPayment).mockRejectedValueOnce(Error('timeout'))
  const h = renderHook(useEpayIntent)
  await act(async () => {
    await h.result.current.pay(10, 'alipay', 7300, gateway)
  })
  const id = vi.mocked(requestPayment).mock.calls[0][0].intent_id
  vi.mocked(getEpayIntent).mockRejectedValue({
    response: { status: 404, data: 'gateway missing' },
  })
  await act(async () => {
    expect(await h.result.current.pay(11, 'wxpay', 8030, gateway)).toBe(false)
  })
  expect(requestPayment).toHaveBeenCalledTimes(1)
  expect(h.result.current.recovery?.status).toBe('unknown')
  vi.mocked(getEpayIntent).mockRejectedValue({
    response: { status: 404, data: { code: 'epay_intent_not_found' } },
  })
  await act(async () => {
    expect(await h.result.current.pay(11, 'wxpay', 8030, gateway)).toBe(true)
  })
  expect(vi.mocked(requestPayment).mock.calls[1][0]).toEqual({
    amount: 11,
    payment_method: 'wxpay',
    expected_minor: 8030,
    intent_id: id,
  })
})

test('only the current quote failure is displayed; neither stale failures nor rejected quotes dispatch checkout', async () => {
  const h = renderHook(usePayment)
  let fail!: (e: Error) => void
  vi.mocked(calculateAmount).mockImplementationOnce(
    () =>
      new Promise((_r, j) => {
        fail = j
      })
  )
  let older!: Promise<number>
  act(() => {
    older = h.result.current.calculatePaymentAmount(10, 'alipay')
  })
  vi.mocked(calculateAmount).mockResolvedValueOnce({
    success: true,
    data: '80.30',
    epay_gateway: gateway,
  })
  await act(async () => {
    expect(await h.result.current.calculatePaymentAmount(11, 'alipay')).toBe(
      80.3
    )
  })
  await act(async () => {
    fail(Error('old quote'))
    await older
  })
  expect(handleServerError).not.toHaveBeenCalled()
  vi.mocked(calculateAmount).mockResolvedValueOnce({
    success: false,
    message: 'quote refused',
  })
  await act(async () => {
    expect(await h.result.current.calculatePaymentAmount(12, 'alipay')).toBe(0)
  })
  expect(handleServerError).toHaveBeenCalledTimes(1)
  expect(h.result.current.isQuoteCurrent(12, 'alipay')).toBe(false)
  await act(async () => {
    expect(await h.result.current.processPayment(12, 'alipay')).toBe(false)
  })
  expect(requestPayment).not.toHaveBeenCalled()
})
test('closing confirmation before signed checkout returns only reconciles the existing order', async () => {
  let resolve!: (r: typeof accepted) => void
  vi.mocked(requestPayment).mockImplementationOnce(
    () =>
      new Promise((r) => {
        resolve = r
      })
  )
  const h = renderHook(useEpayIntent)
  let flight!: Promise<boolean>
  act(() => {
    flight = h.result.current.pay(10, 'alipay', 7300, gateway)
    h.result.current.invalidateEpayHandoff()
  })
  await act(async () => {
    resolve(accepted)
    expect(await flight).toBe(false)
  })
  expect(submitPaymentForm).not.toHaveBeenCalled()
  expect(h.result.current.recovery?.status).toBe('pending')
  expect(requestPayment).toHaveBeenCalledTimes(1)
})
test('a new gateway with the same payment method cannot resume the previous checkout', async () => {
  vi.mocked(requestPayment).mockRejectedValueOnce(Error('lost response'))
  const h = renderHook(useEpayIntent)
  await act(async () => {
    await h.result.current.pay(10, 'alipay', 7300, gateway)
  })
  const id = vi.mocked(requestPayment).mock.calls[0][0].intent_id
  expect(id).toMatch(/^[0-9a-f-]{36}\.a{32}$/)
  await act(async () => {
    expect(await h.result.current.pay(10, 'alipay', 7300, 'b'.repeat(32))).toBe(
      false
    )
  })
  expect(getEpayIntent).toHaveBeenCalledWith(id)
  expect(requestPayment).toHaveBeenCalledTimes(1)
  expect(submitPaymentForm).not.toHaveBeenCalled()
})

const definitiveRotation = (intent_id: string) => ({
  response: {
    status: 409,
    data: { code: 'gateway_changed', intent_id, insert_attempted: false },
  },
})
test('rotation between quote and first submit: a confirmed pre-insert refusal releases only that fresh intent; the new quote needs explicit consent', async () => {
  const revoke = vi.fn()
  vi.mocked(requestPayment).mockImplementationOnce(async ({ intent_id }) => {
    throw definitiveRotation(intent_id)
  })
  const h = renderHook(() => useEpayIntent(revoke))
  await act(async () => {
    expect(await h.result.current.pay(10, 'alipay', 7300, gateway)).toBe(false)
  })
  const original = vi.mocked(requestPayment).mock.calls[0][0].intent_id
  expect(revoke).toHaveBeenCalledOnce()
  expect(h.result.current.recovery).toBeNull()
  expect(getEpayIntent).not.toHaveBeenCalled()
  expect(submitPaymentForm).not.toHaveBeenCalled()
  expect(requestPayment).toHaveBeenCalledTimes(1)
  await act(async () => {
    expect(await h.result.current.pay(10, 'alipay', 7300, 'b'.repeat(32))).toBe(
      true
    )
  })
  expect(vi.mocked(requestPayment).mock.calls[1][0].intent_id).not.toBe(
    original
  )
  expect(submitPaymentForm).toHaveBeenCalledTimes(1)
})
test('a prior ambiguous dispatch cannot be released by a later definitive refusal, nor by a typed missing lookup', async () => {
  vi.mocked(requestPayment)
    .mockRejectedValueOnce(Error('timeout'))
    .mockImplementationOnce(async ({ intent_id }) => {
      throw definitiveRotation(intent_id)
    })
  vi.mocked(getEpayIntent).mockRejectedValue({
    response: {
      status: 404,
      data: { code: 'epay_intent_not_found', reference: trade_no },
    },
  })
  const h = renderHook(useEpayIntent)
  await act(async () => {
    await h.result.current.pay(10, 'alipay', 7300, gateway)
  })
  const original = vi.mocked(requestPayment).mock.calls[0][0].intent_id
  await act(async () => {
    await h.result.current.pay(10, 'alipay', 7300, gateway)
  })
  expect(h.result.current.recovery?.status).toBe('missing')
  expect(h.result.current.recovery?.reference).toBe(trade_no)
  act(() => {
    h.result.current.forgetEpay()
  })
  await act(async () => {
    await h.result.current.pay(10, 'alipay', 7300, 'b'.repeat(32))
  })
  expect(h.result.current.recovery?.draft.id).toBe(original)
  expect(
    vi.mocked(requestPayment).mock.calls.map(([r]) => r.intent_id)
  ).toEqual([original, original])
  expect(submitPaymentForm).not.toHaveBeenCalled()
})
test('an uncorrelated or unproven gateway rejection does not release even a first attempt', async () => {
  vi.mocked(requestPayment).mockRejectedValueOnce(
    definitiveRotation('another-id')
  )
  const h = renderHook(useEpayIntent)
  await act(async () => {
    await h.result.current.pay(10, 'alipay', 7300, gateway)
  })
  expect(h.result.current.recovery?.tradeNo).toBe(trade_no)
  expect(submitPaymentForm).not.toHaveBeenCalled()
})
