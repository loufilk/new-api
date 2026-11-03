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
