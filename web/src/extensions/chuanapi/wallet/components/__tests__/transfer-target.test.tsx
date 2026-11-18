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
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, expect, test, vi } from 'vitest'

import { useSystemConfigStore } from '@/stores/system-config-store'

import { TransferDialog } from '../dialogs/transfer-dialog'

beforeEach(() => {
  useSystemConfigStore.getState().setConfig({
    currency: {
      displayInCurrency: true,
      customCurrencySymbol: '$',
      customCurrencyExchangeRate: 1,
      quotaDisplayType: 'USD',
      quotaPerUnit: 500_000,
      usdExchangeRate: 1,
    },
  })
})

test('an active agent can select agent balance and transfers the same quota amount', async () => {
  const confirm = vi.fn().mockResolvedValue(true)
  render(
    <TransferDialog
      open
      onOpenChange={vi.fn()}
      onConfirm={confirm}
      availableQuota={10_000_000}
      transferring={false}
      allowAgentTarget
    />
  )
  fireEvent.change(screen.getByRole('combobox', { name: 'Transfer target' }), {
    target: { value: 'agent' },
  })
  fireEvent.change(
    screen.getByRole('spinbutton', { name: 'Transfer Amount' }),
    { target: { value: '1.25' } }
  )
  fireEvent.click(screen.getByRole('button', { name: 'Transfer' }))
  await waitFor(() => expect(confirm).toHaveBeenCalledWith(625_000, 'agent'))
})

test('ordinary users have only the paid wallet target and cannot transfer unavailable rewards', () => {
  const confirm = vi.fn()
  render(
    <TransferDialog
      open
      onOpenChange={vi.fn()}
      onConfirm={confirm}
      availableQuota={0}
      transferring={false}
    />
  )
  expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Transfer' })).toBeDisabled()
  expect(confirm).not.toHaveBeenCalled()
})
