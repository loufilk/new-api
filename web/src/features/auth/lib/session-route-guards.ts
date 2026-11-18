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
import { redirect } from '@tanstack/react-router'

import { sanitizeAuthRedirect } from '@/features/auth/lib/auth-redirect'
import {
  AuthSessionUnavailableError,
  resolveAuthentication,
} from '@/lib/auth-session'

export async function requireAuthenticatedSession(input: {
  location: { href: string }
}) {
  // A missing public session hint is not a sign-out verdict. Always resolve
  // here, and admit only a currently authenticated result.
  const outcome = await resolveAuthentication()
  if (outcome.kind === 'transient_error') {
    throw new AuthSessionUnavailableError(outcome)
  }
  if (outcome.kind !== 'authenticated') {
    throw redirect({
      to: '/sign-in',
      search: {
        redirect:
          sanitizeAuthRedirect(input.location.href, window.location.origin) ??
          undefined,
      },
      replace: true,
    })
  }
}

export async function redirectAuthenticatedSession(input: {
  search?: { redirect?: string }
}) {
  const outcome = await resolveAuthentication()
  if (outcome.kind === 'transient_error') {
    throw new AuthSessionUnavailableError(outcome)
  }
  if (outcome.kind === 'authenticated') {
    const target =
      sanitizeAuthRedirect(input.search?.redirect, window.location.origin) ??
      '/dashboard'
    throw redirect({ href: target, replace: true })
  }
}
