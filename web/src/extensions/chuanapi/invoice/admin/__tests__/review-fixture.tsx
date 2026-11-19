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
import { render } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, vi } from 'vitest'

import { api, type ApiRequestConfig } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import InvoiceManagement from '..'
import type { AdminDetail, AdminSummary } from '../api'

export const base = '/api/admin/invoice/applications'
export const summary: AdminSummary = {
  id: 71,
  status: 'pending',
  amount_minor: '9007199254741099',
  currency: 'CNY',
  order_count: 1,
  invoice_type: 'ordinary',
  title_type: 'company',
  category: 'technical_service',
  created_at: 1234,
  updated_at: 1234,
  user_id: 20,
  has_document: false,
}
export const detail: AdminDetail = {
  ...summary,
  title: 'Synthetic invoice title',
  email: 'test@example.invalid',
  orders: [
    {
      id: 19,
      reference: 'TEST-71',
      paid_at: 1200,
      amount_minor: summary.amount_minor,
      type: 'topup',
      currency: 'CNY',
    },
  ],
}
export function setRole(role: number) {
  useAuthStore
    .getState()
    .auth.setUser({ id: 100 + role, username: 'fixture', role })
}
afterEach(() => {
  useAuthStore.getState().auth.reset()
})
export function setup(
  overrides: Partial<AdminDetail> = {},
  role: number = ROLE.SUPER_ADMIN
) {
  setRole(role)
  const client = new QueryClient()
  const read = vi
    .spyOn(api, 'get')
    .mockImplementation(async (url, config: ApiRequestConfig = {}) => {
      if (url === base) {
        const filter = config.params?.status
        const item = {
          ...summary,
          status: overrides.status ?? summary.status,
          fee_state: overrides.fee_state,
          processing: overrides.processing,
          processing_by: overrides.processing_by,
          requires_reconciliation: overrides.requires_reconciliation,
        }
        const include =
          !filter ||
          filter === item.status ||
          (filter === 'refund_review' && item.requires_reconciliation) ||
          (filter === 'fee_refund_review' && item.fee_state === 'refund_review')
        return {
          data: {
            success: true,
            data: { items: include ? [item] : [], total: include ? 1 : 0 },
          },
        }
      }
      if (url === `${base}/71`) {
        return { data: { success: true, data: { ...detail, ...overrides } } }
      }
      throw new Error('Unexpected endpoint')
    })
  const write = vi
    .spyOn(api, 'post')
    .mockResolvedValue({ data: { success: true, data: { id: 71 } } })
  const view = render(
    <QueryClientProvider client={client}>
      <InvoiceManagement />
    </QueryClientProvider>
  )
  return { read, write, view, client, user: userEvent.setup() }
}
