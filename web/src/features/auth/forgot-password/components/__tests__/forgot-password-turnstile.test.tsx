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
import { beforeEach, describe, expect, test, vi } from 'vitest'

const status = vi.hoisted(() => ({
  turnstileCheck: true,
  siteKey: 'test-site-key',
}))

const sendPasswordResetEmail = vi.hoisted(() =>
  vi.fn(async () => ({ success: true, message: '' }))
)

vi.mock('@/features/auth/api', () => ({ sendPasswordResetEmail }))

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
    <button type='button' onClick={() => props.onVerify('verified-token')}>
      complete-{props.action}
    </button>
  ),
}))

const { ForgotPasswordForm } = await import('../forgot-password-form')

beforeEach(() => {
  sendPasswordResetEmail.mockClear()
  status.turnstileCheck = true
  status.siteKey = 'test-site-key'
})

describe('forgot password Turnstile', () => {
  test('submits the verified token with the reset request', async () => {
    const user = userEvent.setup()
    render(<ForgotPasswordForm />)

    await user.type(screen.getByLabelText('Email'), 'user@example.com')
    await user.click(
      screen.getByRole('button', { name: 'complete-reset_password' })
    )
    await user.click(screen.getByRole('button', { name: /Send reset email/ }))

    expect(sendPasswordResetEmail).toHaveBeenCalledWith(
      'user@example.com',
      'verified-token'
    )
  })

  test('skips the challenge entirely when Turnstile is disabled', async () => {
    status.turnstileCheck = false
    status.siteKey = ''
    const user = userEvent.setup()
    render(<ForgotPasswordForm />)

    expect(
      screen.queryByRole('button', { name: /complete-reset_password/ })
    ).not.toBeInTheDocument()

    await user.type(screen.getByLabelText('Email'), 'user@example.com')
    await user.click(screen.getByRole('button', { name: /Send reset email/ }))

    expect(sendPasswordResetEmail).toHaveBeenCalledWith('user@example.com', '')
  })
})
