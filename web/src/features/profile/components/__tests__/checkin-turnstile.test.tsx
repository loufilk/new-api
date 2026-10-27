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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, test, vi } from 'vitest'

const status = vi.hoisted(() => ({
  turnstileCheck: true,
  siteKey: 'test-site-key',
}))

const getCheckinStatus = vi.hoisted(() =>
  vi.fn(async () => ({
    success: true,
    data: {
      enabled: true,
      stats: {
        checked_in_today: false,
        total_checkins: 0,
        total_quota: 0,
        checkin_count: 0,
        records: [],
      },
    },
  }))
)

const performCheckin = vi.hoisted(() =>
  vi.fn(async () => ({
    success: true,
    data: { quota_awarded: 100 },
  }))
)

vi.mock('@/features/profile/api', () => ({ getCheckinStatus, performCheckin }))

vi.mock('@/hooks/use-status', () => ({
  useStatus: () => ({
    status: {
      turnstile_check: status.turnstileCheck,
      turnstile_site_key: status.siteKey,
    },
  }),
}))

vi.mock('@/components/turnstile', () => ({
  Turnstile: (props: { action: string; onVerify: (token: string) => void }) => (
    <button type='button' onClick={() => props.onVerify('checkin-token')}>
      complete-{props.action}
    </button>
  ),
}))

const { CheckinCalendarCard } = await import('../checkin-calendar-card')

function renderCard() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <CheckinCalendarCard checkinEnabled />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  getCheckinStatus.mockClear()
  performCheckin.mockClear()
  status.turnstileCheck = true
  status.siteKey = 'test-site-key'
})

describe('check-in Turnstile', () => {
  test('checks in with the verified token from the confirmation dialog', async () => {
    const user = userEvent.setup()
    renderCard()

    await user.click(
      await screen.findByRole('button', { name: 'Check in now' })
    )

    // The confirmation dialog hosts the challenge; confirm stays disabled
    // until a token arrives.
    const confirmButton = screen.getByRole('button', { name: 'Confirm' })
    expect(confirmButton).toBeDisabled()

    await user.click(screen.getByRole('button', { name: 'complete-checkin' }))
    await waitFor(() => expect(confirmButton).toBeEnabled())

    await user.click(confirmButton)

    await waitFor(() =>
      expect(performCheckin).toHaveBeenCalledWith('checkin-token')
    )
  })

  test('checks in directly without a challenge when Turnstile is disabled', async () => {
    status.turnstileCheck = false
    status.siteKey = ''
    const user = userEvent.setup()
    renderCard()

    await user.click(
      await screen.findByRole('button', { name: 'Check in now' })
    )

    expect(
      screen.queryByRole('button', { name: 'Confirm' })
    ).not.toBeInTheDocument()
    await waitFor(() => expect(performCheckin).toHaveBeenCalledWith(undefined))
  })
})
