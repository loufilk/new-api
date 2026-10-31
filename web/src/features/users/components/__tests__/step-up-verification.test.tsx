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
import type { Row } from '@tanstack/react-table'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useEffect } from 'react'
import { afterEach, expect, it, vi } from 'vitest'

import type { OperationDraft } from '@/features/agent-platform/types'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import type { User } from '../../types'
import { DataTableRowActions } from '../data-table-row-actions'
import { UsersDeleteDialog } from '../users-delete-dialog'
import { UsersProvider, useUsers } from '../users-provider'

const target: User = {
  id: 2,
  username: 'managed-user',
  display_name: 'Managed user',
  role: 1,
  status: 1,
  quota: 0,
  used_quota: 0,
  request_count: 0,
  group: 'default',
}

function mockVerification(scope: string, proofToken: string) {
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/verify/methods') {
      return {
        data: {
          success: true,
          data: {
            scope,
            methods: [{ method: '2fa', available: true }],
            oauth_providers: [],
            password_encryption_enabled: false,
          },
        },
      }
    }
    return { data: { success: true, data: [] } }
  })
  return {
    data: {
      success: true,
      data: {
        proof_token: proofToken,
        method: '2fa',
        scope,
        expires_at: Math.floor(Date.now() / 1000) + 60,
      },
    },
  }
}

function OpenDeleteDialog() {
  const users = useUsers()
  useEffect(() => {
    users.setCurrentRow(target)
    users.setOpen('delete')
    // The harness only seeds provider state once on mount.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  return null
}

function renderInProvider(children: React.ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <UsersProvider>{children}</UsersProvider>
    </QueryClientProvider>
  )
}

async function completeTwoFactorVerification() {
  await userEvent.type(
    await screen.findByLabelText('Authenticator code or backup code'),
    '123456'
  )
  await userEvent.click(screen.getByRole('button', { name: 'Verify' }))
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

it('deleting a user sends the request only with a single-use proof for that user', async () => {
  const proof = mockVerification('admin.user.delete', 'delete-proof')
  const post = vi.spyOn(api, 'post').mockResolvedValue(proof)
  const del = vi
    .spyOn(api, 'delete')
    .mockResolvedValue({ data: { success: true } })
  renderInProvider(
    <>
      <OpenDeleteDialog />
      <UsersDeleteDialog />
    </>
  )
  await userEvent.click(await screen.findByRole('button', { name: 'Delete' }))
  await screen.findByLabelText('Authenticator code or backup code')
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  expect(del).not.toHaveBeenCalled()
  await completeTwoFactorVerification()
  await waitFor(() =>
    expect(del).toHaveBeenCalledWith(
      '/api/user/2/',
      expect.objectContaining({
        headers: { 'X-Security-Proof': 'delete-proof' },
        singleUseAuthorization: true,
      })
    )
  )
  expect(post).toHaveBeenCalledWith(
    '/api/verify',
    expect.objectContaining({
      scope: 'admin.user.delete',
      context: { user_id: 2 },
    }),
    expect.anything()
  )
})

it('cancelling verification leaves the user untouched and returns to the confirmation', async () => {
  mockVerification('admin.user.delete', 'delete-proof')
  const del = vi.spyOn(api, 'delete')
  renderInProvider(
    <>
      <OpenDeleteDialog />
      <UsersDeleteDialog />
    </>
  )
  await userEvent.click(await screen.findByRole('button', { name: 'Delete' }))
  await screen.findByLabelText('Authenticator code or backup code')
  await userEvent.keyboard('{Escape}')
  await screen.findByRole('alertdialog')
  expect(del).not.toHaveBeenCalled()
})

it('disabling a user from the row menu binds the proof to the user and action', async () => {
  const proof = mockVerification('admin.user.manage', 'manage-proof')
  const post = vi.spyOn(api, 'post').mockImplementation(async (url) => {
    if (url === '/api/verify') return proof
    if (url === '/api/user/manage') return { data: { success: true } }
    throw new Error(`Unexpected POST ${url}`)
  })
  renderInProvider(
    <DataTableRowActions row={{ original: target } as Row<User>} />
  )
  await userEvent.click(screen.getByRole('button', { name: 'Open menu' }))
  await userEvent.click(
    await screen.findByRole('menuitem', { name: 'Disable' })
  )
  expect(post).not.toHaveBeenCalledWith(
    '/api/user/manage',
    expect.anything(),
    expect.anything()
  )
  await completeTwoFactorVerification()
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/user/manage',
      { id: 2, action: 'disable' },
      expect.objectContaining({
        headers: { 'X-Security-Proof': 'manage-proof' },
        singleUseAuthorization: true,
      })
    )
  )
  expect(post).toHaveBeenCalledWith(
    '/api/verify',
    expect.objectContaining({
      scope: 'admin.user.manage',
      context: { user_id: 2, action: 'disable' },
    }),
    expect.anything()
  )
})

it.each([1, 5])(
  'role %s uses separate versioned agent eligibility and transaction MFA, not a role switch',
  async (role) => {
    sessionStorage.clear()
    useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role: 100 })
    const proof = mockVerification('agent.transaction', 'profile-one-use')
    const get = vi.mocked(api.get).getMockImplementation()
    vi.mocked(api.get).mockImplementation(async (url, config) => {
      if (url === '/api/agent-platform/profiles/2') {
        return {
          data: {
            success: true,
            data: {
              user_id: 2,
              username: 'managed-user',
              profile:
                role === 5
                  ? {
                      status: 'active',
                      version: 3,
                      sales_frozen: false,
                      settlement_frozen: false,
                    }
                  : null,
            },
          },
        }
      }
      if (!get) throw new Error('Missing verification test fixture')
      return get(url, config)
    })
    const post = vi.spyOn(api, 'post').mockResolvedValue(proof)
    const request = vi
      .spyOn(api, 'request')
      .mockImplementation(async (config) => {
        if (config.url?.endsWith('/prepare')) {
          const input = config.data as OperationDraft
          const context = {
            action: input.action,
            request_id: input.request_id,
            agent_user_id: 2,
            profile_version: role === 5 ? 3 : 0,
            policy_version: 0,
            currency: '',
            amount_minor: '0',
            quota: 0,
            target_type: 'agent_profile',
            target_id: '2',
            target_version: role === 5 ? 3 : 0,
            recipient_version: 0,
            recipient_digest: '',
            parameters_hash: 'profile-parameters',
          }
          return {
            data: {
              success: true,
              data: {
                verification_context: context,
                summary: {
                  action: input.action,
                  agent_user_id: 2,
                  currency: '',
                  currency_exponent: 0,
                  amount_minor: '0',
                  fee_minor: '0',
                  net_minor: '0',
                  quota: 0,
                  target_label: 'managed-user #2',
                  recipient_label: '',
                  policy_version: 0,
                },
                status_url: `/api/agent-platform/operations/${input.request_id}`,
              },
            },
          }
        }
        return { data: { success: true, data: { user_id: 2, version: 4 } } }
      })
    renderInProvider(
      <DataTableRowActions
        row={{ original: { ...target, role } } as Row<User>}
      />
    )
    await userEvent.click(screen.getByRole('button', { name: 'Open menu' }))
    await userEvent.click(
      await screen.findByRole('menuitem', { name: 'Manage agent eligibility' })
    )
    await userEvent.type(
      await screen.findByRole('textbox', { name: 'Reason' }),
      'Reviewed eligibility without banning account'
    )
    await userEvent.click(
      screen.getByRole('checkbox', { name: 'Freeze new sales' })
    )
    await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Authorize transaction' })
      ).toBeEnabled()
    )
    expect(
      request.mock.calls.filter(([config]) => config.method === 'patch')
    ).toHaveLength(0)
    await userEvent.click(
      screen.getByRole('button', { name: 'Authorize transaction' })
    )
    await userEvent.type(
      await screen.findByRole('textbox', { name: 'Authenticator code' }),
      '123456'
    )
    await userEvent.click(screen.getByRole('button', { name: 'Verify' }))
    await waitFor(() =>
      expect(request).toHaveBeenCalledWith(
        expect.objectContaining({
          method: 'patch',
          url: '/api/agent-platform/profiles/2',
          data: {
            request_id: expect.any(String),
            expected_version: role === 5 ? 3 : 0,
            status: 'active',
            sales_frozen: true,
            settlement_frozen: false,
            reason: 'Reviewed eligibility without banning account',
          },
          headers: { 'X-Security-Proof': 'profile-one-use' },
          singleUseAuthorization: true,
        })
      )
    )
    expect(post).toHaveBeenCalledWith(
      '/api/verify',
      expect.objectContaining({
        scope: 'agent.transaction',
        context: expect.objectContaining({
          action: 'profile.update',
          agent_user_id: 2,
          profile_version: role === 5 ? 3 : 0,
          parameters_hash: 'profile-parameters',
        }),
      }),
      expect.anything()
    )
    expect(
      post.mock.calls.filter(([url]) => url === '/api/user/manage')
    ).toHaveLength(0)
  }
)
