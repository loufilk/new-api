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
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AxiosRequestConfig } from 'axios'
import { expect, vi } from 'vitest'

import type { OperationDraft } from '@/features/agent-platform/types'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'
import { useSystemConfigStore } from '@/stores/system-config-store'

/** Network boundary only: real prepare validation, review, and MFA components remain under test. */
export function transactionNetwork() {
  const previousGet = vi.isMockFunction(api.get)
    ? (vi.mocked(api.get).getMockImplementation() ?? api.get.bind(api))
    : api.get.bind(api)
  const previousPost = vi.isMockFunction(api.post)
    ? (vi.mocked(api.post).getMockImplementation() ?? api.post.bind(api))
    : api.post.bind(api)
  const mutations = vi
    .fn<(config: AxiosRequestConfig) => Promise<{ data: unknown }>>()
    .mockResolvedValue({ data: { success: true, data: 1 } })
  const prepare = vi.fn((draft: OperationDraft) => {
    const quota =
      Math.abs(Number(draft.payload.delta ?? draft.payload.quota ?? 0)) *
      Number(draft.payload.count ?? 1)
    const context = {
      action: draft.action,
      request_id: draft.request_id,
      agent_user_id: Number(
        draft.payload.agent_user_id ||
          useAuthStore.getState().auth.user?.id ||
          1
      ),
      profile_version: 1,
      policy_version: 1,
      currency: '',
      amount_minor: '0',
      quota,
      target_type: 'legacy',
      target_id: String(
        draft.payload.id ?? draft.payload.agent_user_id ?? draft.request_id
      ),
      target_version: Number(
        draft.payload.version ?? draft.payload.expected_revision ?? 0
      ),
      recipient_version: 0,
      recipient_digest: '',
      parameters_hash: 'server-bound-parameters',
    }
    return {
      verification_context: context,
      summary: {
        action: context.action,
        agent_user_id: context.agent_user_id,
        currency: '',
        currency_exponent: 0,
        amount_minor: '0',
        fee_minor: '0',
        net_minor: '0',
        quota,
        target_label: `legacy #${context.target_id}`,
        recipient_label: '',
        policy_version: 1,
      },
      status_url: `/api/agent-platform/operations/${draft.request_id}`,
    }
  })
  const verify = vi.fn().mockImplementation(async () => ({
    data: {
      success: true,
      data: {
        proof_token: 'test-single-use-proof',
        scope: 'agent.transaction',
        method: '2fa',
        expires_at: Math.floor(Date.now() / 1000) + 60,
      },
    },
  }))
  vi.spyOn(api, 'get').mockImplementation((url, config) => {
    if (url === '/api/verify/methods') {
      return Promise.resolve({
        data: {
          success: true,
          data: {
            scope: 'agent.transaction',
            methods: [{ method: '2fa', available: true }],
            oauth_providers: [],
            password_encryption_enabled: false,
          },
        },
      })
    }
    if (url === '/api/status') {
      const currency = useSystemConfigStore.getState().config.currency
      return Promise.resolve({
        data: {
          success: true,
          data: {
            display_in_currency: currency.displayInCurrency,
            quota_display_type: currency.quotaDisplayType,
            quota_per_unit: currency.quotaPerUnit,
            usd_exchange_rate: currency.usdExchangeRate,
            custom_currency_symbol: currency.customCurrencySymbol,
            custom_currency_exchange_rate: currency.customCurrencyExchangeRate,
          },
        },
      })
    }
    return previousGet(url, config)
  })
  vi.spyOn(api, 'post').mockImplementation((url, data, config) =>
    url === '/api/verify'
      ? verify(data, config)
      : previousPost(url, data, config)
  )
  vi.spyOn(api, 'request').mockImplementation(async (config) => {
    if (config.url === '/api/agent-platform/operations/prepare') {
      return {
        data: { success: true, data: prepare(config.data as OperationDraft) },
      }
    }
    return mutations(config)
  })
  return { mutations, prepare, verify }
}

export async function authorizeTransaction() {
  const review = await screen.findByRole('alertdialog', {
    name: 'Review transaction',
  })
  const authorize = within(review).getByRole('button', {
    name: 'Authorize transaction',
  })
  await waitFor(() => expect(authorize).toBeEnabled())
  await userEvent.click(authorize)
  fireEvent.change(
    await screen.findByRole('textbox', { name: 'Authenticator code' }),
    { target: { value: '123456' } }
  )
  await userEvent.click(screen.getByRole('button', { name: 'Verify' }))
}
