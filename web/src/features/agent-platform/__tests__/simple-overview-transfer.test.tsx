import { fireEvent, screen, waitFor } from '@testing-library/react'
import { beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { AgentPortal } from '../index'
import {
  mountExpansion,
  ok,
  page,
  setupExpansionUser,
} from './expansion-fixtures'

vi.mock('@/extensions', async (importOriginal) => ({
  ...(await importOriginal<Record<string, unknown>>()),
  extensionWalletPage: () => ({ page: () => null }),
}))

beforeEach(() => {
  setupExpansionUser(1)
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...useSystemConfigStore.getState().config.currency,
      quotaPerUnit: 500_000,
    },
  })
})

test.each([
  { status: 'active', debt: 5_000_000, recovery: 0, enabled: true },
  { status: 'active', debt: 5_000_000, recovery: 10_000_000, enabled: false },
  { status: 'paused', debt: 0, recovery: 0, enabled: false },
])(
  'overview transfers only income remaining after debt: $status/$debt/$recovery',
  async ({ status, debt, recovery, enabled }) => {
    vi.mocked(api.get).mockImplementation(async (url) => {
      if (String(url).endsWith('/self')) return ok({ profile: {} })
      if (String(url).endsWith('/simple/balance')) {
        return ok({
          agent_user_id: 42,
          status,
          available_quota: 0,
          available_credit_quota: 0,
          amount_due_quota: debt,
          payment_available_quota: 15_000_000,
          payment_debt_quota: recovery,
        })
      }
      return ok(page([]))
    })
    await mountExpansion(<AgentPortal />)
    const transfer = await screen.findByRole('button', {
      name: 'Transfer payment income to agent balance',
    })
    if (!enabled) {
      expect(transfer).toBeDisabled()
      return
    }
    fireEvent.click(transfer)
    expect(
      await screen.findByText('Payment income available: $20')
    ).toBeVisible()
    const amount = screen.getByRole('textbox', { name: 'Amount ($)' })
    fireEvent.change(amount, { target: { value: '20.01' } })
    fireEvent.click(screen.getByRole('button', { name: 'Transfer' }))
    expect(await screen.findByText('Enter a valid amount')).toBeVisible()
    expect(api.request).not.toHaveBeenCalled()
    fireEvent.change(amount, { target: { value: '20' } })
    fireEvent.click(screen.getByRole('button', { name: 'Transfer' }))
    await waitFor(() => expect(api.request).toHaveBeenCalledOnce())
    expect(vi.mocked(api.request).mock.calls[0][0]).toMatchObject({
      url: '/api/agent-platform/simple/payment/transfer',
      data: { quota: 10_000_000 },
    })
  }
)
