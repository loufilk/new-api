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
import { z } from 'zod'

import { api } from '@/lib/api'

const chain = z.enum(['bsc', 'polygon', 'tron'])
const cny = z.string().regex(/^\d{1,12}(\.\d{1,2})?$/)
const usdt = z.string().regex(/^\d{1,16}(\.\d{1,18})?$/)
const optionalAmount = (schema: z.ZodString) =>
  z.union([schema, z.literal('')]).nullish()

const optionsSchema = z.object({
  enabled: z.boolean(),
  networks: z
    .array(
      z.object({
        id: chain,
        name: z.string().min(1).max(64),
        bonus_bps: z.number().int().min(0).max(10_000),
      })
    )
    .nullish(),
  min_amount: optionalAmount(cny),
  max_amount: optionalAmount(cny),
  currency: z.literal('CNY').optional(),
})

const orderSchema = z.object({
  id: z.string().min(1).max(128),
  network: chain,
  amount: cny,
  usdt_amount: optionalAmount(usdt),
  wallet_credit: optionalAmount(cny),
  bonus_credit: optionalAmount(cny),
  status: z.enum([
    'creating',
    'pending',
    'confirming',
    'paid',
    'expired',
    'review',
    'failed',
  ]),
  address: z.string().max(128).nullish(),
  expires_at: z.string().max(64).nullish(),
  tx_id: z.string().max(160).nullish(),
})

const draftSchema = z.object({
  network: chain,
  amount: cny,
  idempotency_key: z.uuid(),
})

export type UsdtNetwork = z.infer<typeof chain>
export type UsdtOrder = z.infer<typeof orderSchema>
export type UsdtDraft = z.infer<typeof draftSchema>

const path = '/api/user/usdt'
const silent = { skipBusinessError: true, skipErrorHandler: true }
function read<T>(schema: z.ZodType<T>, payload: unknown) {
  return z.object({ success: z.literal(true), data: schema }).parse(payload)
    .data
}

export async function usdtOptions(signal?: AbortSignal) {
  const { data } = await api.get<unknown>(`${path}/options`, {
    ...silent,
    signal,
  })
  return read(optionsSchema, data)
}

export async function usdtOrders(signal?: AbortSignal) {
  const { data } = await api.get<unknown>(`${path}/orders`, {
    ...silent,
    signal,
  })
  return read(z.array(orderSchema), data)
}

export async function usdtOrder(id: string, signal?: AbortSignal) {
  const { data } = await api.get<unknown>(
    `${path}/orders/${encodeURIComponent(id)}`,
    { ...silent, signal }
  )
  const order = read(orderSchema, data)
  if (order.id !== id) throw new Error('Unexpected payment order')
  return order
}

export class UsdtTermsRejected extends Error {}

export async function lookupUsdtRequest(draft: UsdtDraft) {
  const { data } = await api.get<unknown>(
    `${path}/order-requests/${encodeURIComponent(draft.idempotency_key)}`,
    silent
  )
  const receipt = read(
    z.object({
      idempotency_key: z.literal(draft.idempotency_key),
      order: orderSchema,
    }),
    data
  )
  return checkOrderTerms(receipt.order, draft)
}

export async function postUsdtOrder(draft: UsdtDraft) {
  const { data, status } = await api.post<unknown>(`${path}/orders`, draft, {
    ...silent,
    timeout: 30_000,
    // Only the application's typed, request-bound admission refusal is final.
    // All network, upstream/proxy and unrecognized error responses stay unknown.
    validateStatus: (status) =>
      (status >= 200 && status < 300) || status === 400,
  })
  if (status === 400) {
    const rejected = z
      .object({
        success: z.literal(false),
        code: z.literal('usdt_terms_rejected_before_insert'),
        idempotency_key: z.literal(draft.idempotency_key),
        network: z.literal(draft.network),
        amount: z.literal(draft.amount),
      })
      .safeParse(data)
    if (rejected.success) throw new UsdtTermsRejected()
    throw new Error('Unconfirmed USDT order request')
  }
  return checkOrderTerms(read(orderSchema, data), draft)
}

function checkOrderTerms(order: UsdtOrder, draft: UsdtDraft) {
  if (
    order.network !== draft.network ||
    cents(order.amount) !== cents(draft.amount)
  ) {
    throw new Error('Unexpected payment order terms')
  }
  return order
}

function cents(value: string): bigint | undefined {
  const m = /^(\d{1,12})(?:\.(\d{1,2}))?$/.exec(value)
  if (m) return BigInt(m[1]) * 100n + BigInt((m[2] || '').padEnd(2, '0'))
}

export function withinCnyLimits(
  amount: string,
  min?: string | null,
  max?: string | null
) {
  const a = cents(amount),
    l = cents(min ?? ''),
    h = cents(max ?? '')
  return (
    a !== undefined &&
    l !== undefined &&
    h !== undefined &&
    a > 0n &&
    a >= l &&
    a <= h
  )
}

export function validUsdtAddress(
  network: UsdtNetwork,
  address?: string | null
): address is string {
  if (!address) return false
  return network === 'tron'
    ? /^T[1-9A-HJ-NP-Za-km-z]{33}$/.test(address)
    : /^0x[0-9a-fA-F]{40}$/.test(address)
}

const storageKey = (id: number) => `wallet-usdt-unconfirmed:${id}`

export function loadUsdtDraft(id: number) {
  try {
    const entry = sessionStorage.getItem(storageKey(id))
    if (entry) return draftSchema.parse(JSON.parse(entry))
  } catch {
    /* No repost on load, including corrupt storage. */
  }
}

export function saveUsdtDraft(id: number, draft?: UsdtDraft) {
  try {
    // The stored data excludes the address, hosted link, order and authentication.
    if (draft) sessionStorage.setItem(storageKey(id), JSON.stringify(draft))
    else sessionStorage.removeItem(storageKey(id))
  } catch {
    /* In-memory draft still protects retries on this page. */
  }
}

export const liveUsdtOrder = (order: UsdtOrder) =>
  ['creating', 'pending', 'confirming'].includes(order.status)

export const payableUsdtOrder = (order: UsdtOrder, now = Date.now()) =>
  order.status === 'pending' &&
  Date.parse(order.expires_at ?? '') > now &&
  !!order.usdt_amount &&
  /[1-9]/.test(order.usdt_amount)
