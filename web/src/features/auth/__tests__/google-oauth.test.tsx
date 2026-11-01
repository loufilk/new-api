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
import { act, render, renderHook, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { toast } from 'sonner'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { buildOAuthAuthorizationUrl } from '@/lib/oauth'
import { AuthOperationError } from '@/lib/secure-verification'

import { OAuthProviders } from '../components/oauth-providers'
import { useOAuthLogin } from '../hooks/use-oauth-login'
import { consumeOAuthLoginRedirect } from '../lib/oauth-callback-mode'

const ready = { google_oauth: true, google_oauth_configured: true }
const authorizationURL =
  'https://accounts.google.com/o/oauth2/v2/auth?state=google-state&code_challenge=challenge&nonce=nonce'

beforeEach(() => {
  window.sessionStorage.clear()
  vi.spyOn(window, 'localStorage', 'get').mockReturnValue(window.sessionStorage)
})

afterEach(() => vi.restoreAllMocks())

describe('Google sign-in', () => {
  it.each([
    [
      'GOOGLE_OAUTH_FAILED',
      'Google authorization failed. Please start Google sign-in again.',
    ],
    [
      'GOOGLE_REAUTHENTICATION_REQUIRED',
      'Google requires a recent sign-in for this security action. Sign in to Google again, or use a password, two-factor authentication or Passkey.',
    ],
  ])(
    'maps %s to safe actionable guidance rather than upstream details',
    (code, message) => {
      const failure = AuthOperationError.from({
        isAxiosError: true,
        response: {
          status: 400,
          data: { code, message: 'sensitive provider details' },
        },
      })
      expect(failure.message).toBe(message)
    }
  )
  it.each([
    { google_oauth: false, google_oauth_configured: true },
    { google_oauth: true, google_oauth_configured: false },
    {},
  ])('does not offer sign-in when disabled or incomplete: %j', (status) => {
    render(<OAuthProviders status={status} />)
    expect(
      screen.queryByRole('button', { name: 'Continue with Google' })
    ).not.toBeInTheDocument()
  })

  it('uses the server-owned authorization URL only after signing out and preserves a safe destination', async () => {
    const post = vi.spyOn(api, 'post').mockImplementation(async (url) => ({
      data:
        url === '/api/user/auth/logout'
          ? { success: true }
          : {
              success: true,
              data: {
                flow_token: 'google-state',
                authorization_url: authorizationURL,
              },
            },
    }))
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    const user = userEvent.setup()
    render(<OAuthProviders status={ready} redirectTo='/console/personal' />)
    await user.click(
      screen.getByRole('button', { name: 'Continue with Google' })
    )
    expect(post.mock.calls.map(([url]) => url)).toEqual([
      '/api/user/auth/logout',
      '/api/oauth/state',
    ])
    expect(post).toHaveBeenCalledWith(
      '/api/oauth/state',
      expect.objectContaining({ provider: 'google', intent: 'login' }),
      expect.anything()
    )
    expect(open).toHaveBeenCalledWith(authorizationURL, '_self')
    expect(consumeOAuthLoginRedirect('google-state')).toBe('/console/personal')
    expect(consumeOAuthLoginRedirect('google-state')).toBeNull()
  })

  it('keeps legal-consent and different-origin restrictions on the Google button', () => {
    const view = render(<OAuthProviders status={ready} disabled />)
    expect(
      screen.getByRole('button', { name: 'Continue with Google' })
    ).toBeDisabled()
    view.rerender(
      <OAuthProviders
        status={{ ...ready, server_address: 'https://another-site.example' }}
      />
    )
    expect(
      screen.queryByRole('button', { name: 'Continue with Google' })
    ).not.toBeInTheDocument()
  })

  it('does not duplicate pending starts and allows retry after a failed state request', async () => {
    let finish!: (value: { data: { success: boolean } }) => void
    const post = vi.spyOn(api, 'post').mockImplementation((url) => {
      if (url === '/api/user/auth/logout') {
        return Promise.resolve({ data: { success: true } })
      }
      return new Promise((resolve) => {
        finish = resolve
      })
    })
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    const error = vi.spyOn(toast, 'error')
    const { result } = renderHook(() => useOAuthLogin(ready))
    let started!: Promise<void>
    await act(async () => {
      started = result.current.handleGoogleLogin()
      await result.current.handleGoogleLogin()
    })
    expect(
      post.mock.calls.filter(([url]) => url === '/api/oauth/state')
    ).toHaveLength(1)
    expect(result.current.isLoading).toBe(true)
    await act(async () => {
      finish({ data: { success: false } })
      await started
    })
    expect(result.current.isLoading).toBe(false)
    expect(open).not.toHaveBeenCalled()
    expect(error).toHaveBeenCalledTimes(1)
  })

  it('does not navigate or reconstruct an authorization request when the server URL is missing', async () => {
    vi.spyOn(api, 'post').mockImplementation(async (url) => ({
      data:
        url === '/api/user/auth/logout'
          ? { success: true }
          : { success: true, data: { flow_token: 'google-state' } },
    }))
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    const { result } = renderHook(() => useOAuthLogin(ready))
    await act(() => result.current.handleGoogleLogin())
    expect(open).not.toHaveBeenCalled()
    expect(() =>
      buildOAuthAuthorizationUrl('google', 'state', {
        custom_oauth_providers: [
          {
            id: 1,
            slug: 'google',
            name: 'Collision',
            icon: '',
            client_id: 'client',
            authorization_endpoint: 'https://untrusted.example',
            scopes: '',
          },
        ],
      })
    ).toThrow('No linked OAuth provider is available.')
  })
})
