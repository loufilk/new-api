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
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { PoolDiagnostics } from '../diagnostics'

function renderDiagnostics(role: number) {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'fixture', role })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  render(
    <QueryClientProvider client={client}>
      <PoolDiagnostics />
    </QueryClientProvider>
  )
}
beforeEach(async () => {
  i18next.addResourceBundle('en', 'translation', en.translation, true, true)
  await i18next.changeLanguage('en')
})
afterEach(() => useAuthStore.getState().auth.reset())
describe('root-only pool diagnostics', () => {
  test.each([ROLE.USER, 5, ROLE.ADMIN])(
    'role %s never sees diagnostics or requests the root API',
    (role) => {
      const get = vi.spyOn(api, 'get')
      renderDiagnostics(role)
      expect(screen.queryByRole('button')).not.toBeInTheDocument()
      expect(get).not.toHaveBeenCalled()
    }
  )
  test('root explicitly expands read-only diagnostics before fetching operational status', async () => {
    const get = vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: {
          configured: true,
          invalid_config: false,
          nodes: [
            {
              channel_id: 42,
              state: 'unavailable',
              count: 0,
              error_code: 'management_read_access_not_configured',
            },
          ],
          cache_scope: 'per_process',
          cache_ttl_seconds: 60,
          stale_after_seconds: 300,
          automatic_collection: false,
        },
      },
    })
    const user = userEvent.setup()
    renderDiagnostics(ROLE.SUPER_ADMIN)
    const trigger = screen.getByRole('button', {
      name: 'Root-only pool diagnostics',
    })
    expect(trigger).toHaveAttribute('aria-expanded', 'false')
    expect(get).not.toHaveBeenCalled()
    await user.click(trigger)
    expect(trigger).toHaveAttribute('aria-expanded', 'true')
    expect(
      await screen.findByText('Management read access not configured')
    ).toBeVisible()
    expect(get).toHaveBeenCalledWith(
      '/api/option/account-pool',
      expect.any(Object)
    )
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
  })
})
