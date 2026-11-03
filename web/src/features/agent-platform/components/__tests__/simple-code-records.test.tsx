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
import type { ReactNode } from 'react'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { SimpleCodeRecords } from '../simple-code-records'

const activeRecord = {
  id: 11,
  agent_user_id: 42,
  name: 'Customer test code',
  key: 'ACTIVE-CODE-SECRET',
  masked_key: 'ACTIVE********SECRET',
  quota: 500_000,
  mode: 'direct' as const,
  status: 1,
  expired_at: 0,
  redeemed_at: 0,
  created_at: 1_800_000_000,
  version: 1,
  cost_quota: 450_000,
}

function mount(node: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>{node}</QueryClientProvider>
  )
}

beforeEach(() => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: { items: [activeRecord], total: 1, page: 1, page_size: 20 },
    },
  })
  vi.spyOn(api, 'request').mockResolvedValue({
    data: {
      success: true,
      data: { ...activeRecord, key: undefined, status: 2 },
    },
  })
})

describe('simple redemption code records', () => {
  test('shows active code and asks for confirmation before cancellation', async () => {
    mount(<SimpleCodeRecords agentUserId={42} />)

    expect(await screen.findByText('ACTIVE-CODE-SECRET')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Cancel code' }))
    expect(
      screen.getByText(/Only cancel if this code has not been given/)
    ).toBeInTheDocument()
    expect(api.request).not.toHaveBeenCalled()

    fireEvent.click(
      within(screen.getByRole('alertdialog')).getByRole('button', {
        name: 'Cancel code',
      })
    )
    await waitFor(() => expect(api.request).toHaveBeenCalledOnce())
    await waitFor(() =>
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    )
    expect(vi.mocked(api.request).mock.calls[0][0]).toMatchObject({
      method: 'post',
      url: '/api/agent-platform/simple/codes/11/cancel',
      data: {
        version: 1,
        reason: 'Agent cancelled an undelivered code',
      },
    })
  })

  test('uses a masked code and hides cancellation when a credential is omitted', async () => {
    vi.mocked(api.get).mockResolvedValue({
      data: {
        success: true,
        data: {
          items: [{ ...activeRecord, key: undefined, status: 3 }],
          total: 1,
          page: 1,
          page_size: 20,
        },
      },
    })
    mount(<SimpleCodeRecords agentUserId={42} />)

    expect(await screen.findByText('ACTIVE********SECRET')).toBeInTheDocument()
    expect(screen.queryByText('ACTIVE-CODE-SECRET')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Cancel code' })
    ).not.toBeInTheDocument()
  })
})
