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
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { SimpleAgentFinance } from '../simple-finance'

test('wallet-funded exit defaults to original wallet and rejects a cash refund', async () => {
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...useSystemConfigStore.getState().config.currency,
      quotaPerUnit: 500_000,
    },
  })
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        items: [
          {
            agent_user_id: 42,
            username: 'Agent',
            status: 'paused',
            available_quota: 500_000,
            wallet_quota: 500_000,
            available_credit_quota: 0,
            amount_due_quota: 0,
            overdue_quota: 0,
          },
        ],
        total: 1,
      },
    },
  })
  const request = vi
    .spyOn(api, 'request')
    .mockResolvedValue({ data: { success: true, data: {} } })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SimpleAgentFinance />
    </QueryClientProvider>
  )
  fireEvent.click(
    await screen.findByRole('button', { name: 'Settle remaining balance' })
  )
  fireEvent.change(screen.getByRole('textbox', { name: 'Amount ($)' }), {
    target: { value: '1' },
  })
  fireEvent.change(screen.getByRole('combobox', { name: 'Target' }), {
    target: { value: 'refund' },
  })
  fireEvent.change(screen.getByRole('combobox', { name: 'Source' }), {
    target: { value: 'wallet' },
  })
  expect(screen.getByRole('combobox', { name: 'Target' })).toHaveValue('wallet')
  fireEvent.change(screen.getByRole('combobox', { name: 'Target' }), {
    target: { value: 'refund' },
  })
  for (const name of ['Reason', 'Support evidence']) {
    fireEvent.change(screen.getByRole('textbox', { name }), {
      target: { value: 'Synthetic exit' },
    })
  }
  fireEvent.click(
    screen.getByRole('checkbox', {
      name: 'I confirm this change and its financial impact',
    })
  )
  fireEvent.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() =>
    expect(screen.getByRole('combobox', { name: 'Target' })).toHaveAttribute(
      'aria-invalid',
      'true'
    )
  )
  expect(request).not.toHaveBeenCalled()
  fireEvent.change(screen.getByRole('combobox', { name: 'Target' }), {
    target: { value: 'wallet' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(request).toHaveBeenCalledOnce())
  expect(request.mock.calls[0][0]).toMatchObject({
    url: '/api/agent-platform/simple/finance/exit',
    data: { source: 'wallet', target: 'wallet', quota: 500_000 },
  })
})
