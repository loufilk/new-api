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
import i18next from 'i18next'
import { afterEach, expect, test } from 'vitest'

import { SYSTEM_SETTINGS_VIEW } from '@/components/layout/config/system-settings.config'
import { Route } from '@/routes/_authenticated/system-settings/route'
import { useAuthStore } from '@/stores/auth-store'

const guard = Route.options.beforeLoad as (args: {
  location: { pathname: string }
}) => unknown
afterEach(() => useAuthStore.getState().auth.reset())

test.each([1, 5, 10, 100])(
  'role %s has access only to permitted settings routes',
  (role) => {
    useAuthStore.getState().auth.setUser({ id: 7, username: 'admin', role })
    const fingerprint = () =>
      guard({ location: { pathname: '/system-settings/content/model-tests' } })
    if (role >= 10) expect(fingerprint).not.toThrow()
    else expect(fingerprint).toThrow()
    for (const pathname of [
      '/system-settings/site/branding',
      '/system-settings/content/support-email',
      '/system-settings/content/model-tests/extra',
      '/system-settings/content/dashboard',
    ]) {
      const restricted = () => guard({ location: { pathname } })
      if (role === 100) expect(restricted).not.toThrow()
      else expect(restricted).toThrow()
    }
  }
)

test('ordinary admin settings sidebar shows only fingerprint configuration', () => {
  useAuthStore.getState().auth.setUser({ id: 7, username: 'admin', role: 10 })
  const groups = SYSTEM_SETTINGS_VIEW.getNavGroups(i18next.t, {})
  expect(groups).toHaveLength(1)
  expect(groups[0].items).toHaveLength(1)
  expect(groups[0].items[0]).toMatchObject({
    url: '/system-settings/content/model-tests',
  })
})

test('ordinary users cannot discover any settings entries', () => {
  useAuthStore.getState().auth.setUser({ id: 7, username: 'user', role: 1 })
  expect(SYSTEM_SETTINGS_VIEW.getNavGroups(i18next.t, {})).toEqual([])
})
