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
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, test, vi } from 'vitest'

import { useSystemConfigStore } from '@/stores/system-config-store'

import { WithdrawalContactDialog } from '../withdrawal-contact-dialog'

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

test('contact summary separates income sources and explains the common withdrawal rules', async () => {
  const user = userEvent.setup()
  render(
    <WithdrawalContactDialog
      userId={42}
      referralQuota={15_000_000}
      paymentQuota={10_000_000}
      onClose={vi.fn()}
    />
  )
  expect(screen.getByText('$30')).toBeVisible()
  expect(screen.getByText('$20')).toBeVisible()
  expect(screen.getByText('$50')).toBeVisible()
  expect(
    screen.getByRole('link', { name: 'Contact customer support' })
  ).toHaveAttribute('href', '/support')
  const copyButton = screen.getByRole('button', {
    name: 'Copy withdrawal summary',
  })
  await user.click(copyButton)
  expect(await navigator.clipboard.readText()).toContain('User ID: 42')
  expect(await navigator.clipboard.readText()).toContain(
    'Referral Rewards Available: $30'
  )
  expect(await navigator.clipboard.readText()).toContain(
    'Payment income available: $20'
  )
  expect(await navigator.clipboard.readText()).toContain(
    'Minimum $20 combined, once per calendar month, with a 2% fee.'
  )
  expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
  expect(copyButton).toHaveClass('w-full', 'sm:w-auto')
})

test('estimates debt offsets from payment income first and excludes frozen amounts', () => {
  render(
    <WithdrawalContactDialog
      userId={42}
      referralQuota={15_000_000}
      paymentQuota={10_000_000}
      amountDue={5_000_000}
      recoveryDebt={2_500_000}
      onClose={vi.fn()}
    />
  )
  expect(screen.getByText('$5')).toBeVisible()
  expect(screen.getByText('$30')).toBeVisible()
  expect(screen.getByText('$35')).toBeVisible()
  expect(
    screen.getByText(
      'Outstanding debt is offset before withdrawal. The final available amount is confirmed by support.'
    )
  ).toBeVisible()
})

test('negative referral recovery does not reduce independent payment income', () => {
  render(
    <WithdrawalContactDialog
      userId={42}
      referralQuota={-500_000}
      paymentQuota={10_000_000}
      onClose={vi.fn()}
    />
  )
  expect(screen.getByText('$0')).toBeVisible()
  expect(screen.getAllByText('$20')).toHaveLength(2)
  expect(screen.queryByRole('status')).not.toBeInTheDocument()
})

test('payment recovery does not reduce independent referral income', () => {
  render(
    <WithdrawalContactDialog
      userId={42}
      referralQuota={15_000_000}
      paymentQuota={0}
      recoveryDebt={2_500_000}
      onClose={vi.fn()}
    />
  )
  expect(screen.getByText('$0')).toBeVisible()
  expect(screen.getAllByText('$30')).toHaveLength(2)
  expect(screen.queryByRole('status')).not.toBeInTheDocument()
})
