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
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { OperationDialog } from '../components/operation-dialog'
import { PendingOperations } from '../components/pending-operations'
import { validatePreparedOperation } from '../lib/operation-validation'
import { pendingOperations, rememberOperation } from '../lib/recovery'
import type { OperationDraft, PreparedOperation } from '../types'

const draft: OperationDraft = {
  action: 'commission.withdraw.request',
  request_id: 'request-123',
  payload: { quote_id: 'locked-quote' },
  endpoint: '/withdrawals',
  resource: '/withdrawals',
}
const prepared: PreparedOperation = {
  verification_context: {
    action: draft.action,
    request_id: draft.request_id,
    agent_user_id: 42,
    profile_version: 3,
    policy_version: 7,
    currency: 'USD',
    amount_minor: '9007199254740991',
    quota: 0,
    target_type: 'withdrawal_quote',
    target_id: 'locked-quote',
    target_version: 1,
    recipient_version: 2,
    recipient_digest: 'digest',
    parameters_hash: 'server-only-hash',
  },
  summary: {
    action: draft.action,
    agent_user_id: 42,
    currency: 'USD',
    currency_exponent: 2,
    amount_minor: '9007199254740991',
    fee_minor: '100',
    net_minor: '9007199254740891',
    quota: 0,
    target_label: 'Locked withdrawal',
    recipient_label: 'Finance file ••123 v2',
    policy_version: 7,
  },
  status_url: '/api/agent-platform/operations/request-123',
}
const ok = (data: unknown) => ({ data: { success: true, data } })
function renderOperation(operation = draft) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  client.setQueryData(['status'], {})
  const onClose = vi.fn(),
    onComplete = vi.fn()
  const view = render(
    <QueryClientProvider client={client}>
      <OperationDialog
        draft={operation}
        onClose={onClose}
        onComplete={onComplete}
      />
    </QueryClientProvider>
  )
  return { ...view, onClose, onComplete }
}
async function authorize() {
  const review = await screen.findByRole('button', {
    name: 'Authorize transaction',
  })
  await waitFor(() => expect(review).toBeEnabled())
  fireEvent.click(review)
  const code = await screen.findByRole('textbox', {
    name: 'Authenticator code',
  })
  fireEvent.change(code, { target: { value: '123456' } })
  fireEvent.click(screen.getByRole('button', { name: 'Verify' }))
}
beforeEach(() => {
  sessionStorage.clear()
  useAuthStore
    .getState()
    .auth.setUser({ id: 42, username: 'customer', role: 1 })
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/verify/methods') {
      return ok({
        scope: 'agent.transaction',
        methods: [{ method: '2fa', available: true }],
        oauth_providers: [],
        password_encryption_enabled: false,
      })
    }
    return ok({ status: 'not_observed', request_id: draft.request_id })
  })
  vi.spyOn(api, 'post').mockResolvedValue(
    ok({
      proof_token: 'one-use-secret',
      scope: 'agent.transaction',
      method: '2fa',
      expires_at: Math.floor(Date.now() / 1000) + 60,
    })
  )
  vi.spyOn(api, 'request').mockImplementation(async (config) =>
    config.url?.endsWith('/prepare')
      ? ok(prepared)
      : ok({ id: 100, status: 'applied' })
  )
})
describe('concrete financial transaction authorization', () => {
  test('shows exact original money and immutable recipient before verification and submits once with bound proof', async () => {
    const { onComplete } = renderOperation()
    await screen.findByText('Finance file ••123 v2')
    expect(screen.getByText(/90,071,992,547,409\.91/)).toBeInTheDocument()
    await authorize()
    await waitFor(() =>
      expect(onComplete).toHaveBeenCalledWith({ id: 100, status: 'applied' })
    )
    expect(api.post).toHaveBeenCalledWith(
      '/api/verify',
      expect.objectContaining({
        method: '2fa',
        scope: 'agent.transaction',
        code: '123456',
        context: prepared.verification_context,
      }),
      expect.anything()
    )
    const submissions = vi
      .mocked(api.request)
      .mock.calls.filter(
        ([config]) => config.url === '/api/agent-platform/withdrawals'
      )
    expect(submissions).toHaveLength(1)
    expect(submissions[0][0]).toMatchObject({
      data: { request_id: draft.request_id, quote_id: 'locked-quote' },
      headers: { 'X-Security-Proof': 'one-use-secret' },
      skipAuthRefresh: true,
    })
    expect(pendingOperations(42)).toEqual([])
    expect(JSON.stringify(sessionStorage)).not.toContain('one-use-secret')
  })
  test('cancel before verification never obtains or consumes proof', async () => {
    const { onClose } = renderOperation()
    await screen.findByText('Locked withdrawal')
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(onClose).toHaveBeenCalledOnce()
    expect(api.post).not.toHaveBeenCalled()
    expect(api.request).toHaveBeenCalledTimes(1)
  })
  test('rejects backup codes and ignores password/session fallback for transaction scope', async () => {
    vi.mocked(api.get).mockResolvedValue(
      ok({
        scope: 'agent.transaction',
        methods: [
          { method: 'session', available: true },
          { method: 'password', available: true },
          { method: '2fa', available: true },
        ],
        oauth_providers: [],
        password_encryption_enabled: false,
      })
    )
    renderOperation()
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Authorize transaction' })
      ).toBeEnabled()
    )
    fireEvent.click(
      screen.getByRole('button', { name: 'Authorize transaction' })
    )
    const code = await screen.findByRole('textbox', {
      name: 'Authenticator code',
    })
    expect(screen.queryByLabelText('Password')).not.toBeInTheDocument()
    fireEvent.change(code, { target: { value: 'abcdef' } })
    expect(screen.getByRole('button', { name: 'Verify' })).toBeDisabled()
    expect(api.post).not.toHaveBeenCalled()
  })
  test('cancel in MFA never submits the financial operation', async () => {
    renderOperation()
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Authorize transaction' })
      ).toBeEnabled()
    )
    fireEvent.click(
      screen.getByRole('button', { name: 'Authorize transaction' })
    )
    await screen.findByRole('textbox', { name: 'Authenticator code' })
    const dialog = screen.getByRole('dialog')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() =>
      expect(
        screen.queryByRole('textbox', { name: 'Authenticator code' })
      ).not.toBeInTheDocument()
    )
    expect(api.post).not.toHaveBeenCalled()
    expect(api.request).toHaveBeenCalledTimes(1)
  })
  test('lost response keeps only non-secret IDs and reads recovery without retrying a payment', async () => {
    vi.mocked(api.request).mockImplementation(async (config) => {
      if (config.url?.endsWith('/prepare')) return ok(prepared)
      throw new Error('connection lost')
    })
    renderOperation()
    await authorize()
    await screen.findByText('Do not submit a new request')
    expect(pendingOperations(42)).toEqual([
      {
        request_id: draft.request_id,
        action: draft.action,
        resource: draft.resource,
      },
    ])
    expect(sessionStorage.getItem('agent-platform:pending:42')).not.toMatch(
      /one-use-secret|locked-quote|recipient/
    )
    expect(
      screen.getByRole('button', { name: 'Authorize transaction' })
    ).toBeDisabled()
    fireEvent.click(
      screen.getByRole('button', { name: 'Check request status' })
    )
    await screen.findByText(
      'No matching record is visible yet. Keep this request ID and contact finance. Do not create a replacement payment.'
    )
    expect(api.request).toHaveBeenCalledTimes(2)
    expect(api.post).toHaveBeenCalledTimes(1)
    expect(api.get).toHaveBeenCalledWith(
      '/api/agent-platform/operations/request-123',
      expect.anything()
    )
  })
  test('does not authorize a replacement action while the same action remains unresolved', async () => {
    rememberOperation(42, { ...draft, request_id: 'older-unknown' })
    renderOperation()
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Authorize transaction' })
      ).toBeEnabled()
    )
    fireEvent.click(
      screen.getByRole('button', { name: 'Authorize transaction' })
    )
    expect(api.post).not.toHaveBeenCalled()
    expect(api.get).not.toHaveBeenCalled()
  })
  test('shows policy changes alongside the trusted server transaction summary', async () => {
    renderOperation({
      ...draft,
      review: [
        { label: 'Enable new withdrawals', before: 'false', after: 'true' },
        { label: 'Reason', after: 'Approved documented terms' },
      ],
    })
    expect(
      await screen.findByText('Enable new withdrawals')
    ).toBeInTheDocument()
    expect(screen.getByText('Approved documented terms')).toBeInTheDocument()
  })
  test('refuses missing or conflicting summary/context fields instead of authorizing', () => {
    expect(() =>
      validatePreparedOperation(
        { ...prepared, summary: { ...prepared.summary, amount_minor: '1' } },
        draft
      )
    ).toThrow('does not match')
    expect(() =>
      validatePreparedOperation(
        {
          ...prepared,
          verification_context: {
            ...prepared.verification_context,
            recipient_digest: undefined,
          },
        },
        draft
      )
    ).toThrow('incomplete')
    expect(() =>
      validatePreparedOperation(prepared, { ...draft, request_id: 'another' })
    ).toThrow('does not match')
  })
})

test('after a reload, a completed unbound delivery recovers its claim link through a read-only owner request without storing the secret', async () => {
  const operation = {
    request_id: 'delivery-request-9',
    action: 'delivery.create',
    resource: '/deliveries',
  }
  rememberOperation(42, operation)
  vi.mocked(api.get).mockResolvedValue(
    ok({
      request_id: operation.request_id,
      action: operation.action,
      status: 'completed',
      resource_type: 'delivery',
      resource_id: '9',
      resource_state: 'created',
      resource: {
        id: 9,
        quota: 500000,
        claim_code: 'private-recovered-code',
        state: 'created',
      },
    })
  )
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <PendingOperations />
    </QueryClientProvider>
  )
  fireEvent.click(screen.getByRole('button', { name: 'Check request status' }))
  expect(
    await screen.findByRole('button', { name: 'Copy customer claim link' })
  ).toBeInTheDocument()
  expect(api.get).toHaveBeenCalledWith(
    '/api/agent-platform/operations/delivery-request-9',
    expect.anything()
  )
  expect(api.request).not.toHaveBeenCalled()
  expect(api.post).not.toHaveBeenCalled()
  expect(pendingOperations(42)).toEqual([])
  expect(sessionStorage.getItem('agent-platform:pending:42')).not.toContain(
    'private-recovered-code'
  )
})

test('unknown or malformed recovery never clears the persisted financial request', async () => {
  rememberOperation(42, draft)
  vi.mocked(api.get).mockResolvedValue(
    ok({ request_id: 'wrong-owner-request', status: 'completed' })
  )
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <PendingOperations />
    </QueryClientProvider>
  )
  fireEvent.click(screen.getByRole('button', { name: 'Check request status' }))
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Check request status' })
    ).toBeEnabled()
  )
  expect(pendingOperations(42)).toHaveLength(1)
  expect(api.request).not.toHaveBeenCalled()
})
