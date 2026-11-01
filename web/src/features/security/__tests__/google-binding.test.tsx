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
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { toast } from 'sonner'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import type { UserProfile } from '@/features/profile/types'
import { api } from '@/lib/api'

import { AccountBindings } from '../components/account-bindings'

let client: QueryClient
beforeEach(() => {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
})
afterEach(() => {
  client.clear()
  vi.restoreAllMocks()
})

it.each([true, false])(
  'requires fresh verification to unlink Google and preserves binding on server refusal: success=%s',
  async (success) => {
    const get = vi.spyOn(api, 'get').mockImplementation(async (url) => ({
      data: {
        success: true,
        data:
          url === '/api/status'
            ? { google_oauth: false, google_oauth_configured: false }
            : {
                scope: 'account.binding.unbind',
                methods: [{ method: 'password', available: true }],
                oauth_providers: [],
                password_encryption_enabled: false,
              },
      },
    }))
    const post = vi.spyOn(api, 'post').mockResolvedValue({
      data: {
        success: true,
        data: {
          proof_token: 'google-unbind-proof',
          method: 'password',
          scope: 'account.binding.unbind',
          expires_at: Math.floor(Date.now() / 1000) + 300,
        },
      },
    })
    const remove = vi.spyOn(api, 'delete').mockResolvedValue({
      data: success
        ? { success: true, data: { action: 'unbind' } }
        : { success: false, message: 'Keep at least one sign-in method.' },
    })
    const error = vi.spyOn(toast, 'error')
    const onUpdate = vi.fn()
    const user = userEvent.setup()
    render(
      <QueryClientProvider client={client}>
        <AccountBindings
          profile={{ google_id: 'google-subject' } as UserProfile}
          onUpdate={onUpdate}
        />
      </QueryClientProvider>
    )
    const entry = (await screen.findByText('Google')).closest('li')
    if (!entry) throw new Error('Google binding entry missing')
    await user.click(within(entry).getByRole('button', { name: 'Unbind' }))
    const confirmation = await screen.findByRole('alertdialog', {
      name: 'Confirm Unbind',
    })
    expect(remove).not.toHaveBeenCalled()
    await user.click(
      within(confirmation).getByRole('button', { name: 'Confirm Unbind' })
    )
    const verification = await screen.findByRole('dialog', {
      name: 'Security verification',
    })
    expect(remove).not.toHaveBeenCalled()
    await user.type(
      within(verification).getByLabelText('Password', { selector: 'input' }),
      'current-password'
    )
    await user.click(
      within(verification).getByRole('button', { name: 'Verify' })
    )
    await waitFor(() =>
      expect(remove).toHaveBeenCalledWith(
        '/api/user/bindings/google',
        expect.objectContaining({
          headers: { 'X-Security-Proof': 'google-unbind-proof' },
          singleUseAuthorization: true,
          acceptAuthRotation: true,
        })
      )
    )
    expect(get).toHaveBeenCalledWith(
      '/api/verify/methods',
      expect.objectContaining({
        params: expect.objectContaining({ scope: 'account.binding.unbind' }),
      })
    )
    expect(post).toHaveBeenCalledWith(
      '/api/verify',
      expect.objectContaining({
        scope: 'account.binding.unbind',
        context: { provider: 'google' },
      }),
      expect.anything()
    )
    if (success) await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1))
    else {
      await waitFor(() => expect(error).toHaveBeenCalledTimes(1))
      expect(onUpdate).not.toHaveBeenCalled()
      expect(screen.getByText('google-subject')).toBeVisible()
    }
  }
)

it('cancels Google unlink without sending a verification or mutation request', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: { google_oauth: true, google_oauth_configured: true },
    },
  })
  const post = vi.spyOn(api, 'post')
  const remove = vi.spyOn(api, 'delete')
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={client}>
      <AccountBindings
        profile={{ google_id: 'google-subject' } as UserProfile}
        onUpdate={vi.fn()}
      />
    </QueryClientProvider>
  )
  await user.click(await screen.findByRole('button', { name: 'Unbind' }))
  const confirmation = await screen.findByRole('alertdialog', {
    name: 'Confirm Unbind',
  })
  await user.click(within(confirmation).getByRole('button', { name: 'Cancel' }))
  expect(post).not.toHaveBeenCalled()
  expect(remove).not.toHaveBeenCalled()
  expect(screen.getByText('google-subject')).toBeVisible()
})
