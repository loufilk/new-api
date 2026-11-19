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
import { expect, test, vi } from 'vitest'

import { exportLogs } from '../api'

vi.mock('@/lib/api-url', async (original) => ({
  ...(await original<typeof import('@/lib/api-url')>()),
  apiUrl: (path: string) => 'https://api.example.invalid/gateway' + path,
}))
vi.mock('@/lib/api', () => ({
  api: {},
  getFreshAuthHeaders: () =>
    Promise.resolve({ Authorization: 'Bearer fixture' }),
}))

test('sends log export only to the configured backend, including path prefix', async () => {
  const fetchMock = vi
    .fn()
    .mockResolvedValue(new Response('{"success":true,"data":{"count":0}}\n'))
  vi.stubGlobal('fetch', fetchMock)
  try {
    await expect(
      exportLogs({}, false, {
        signal: new AbortController().signal,
        onLog: () => undefined,
      })
    ).resolves.toBe(0)
    expect(fetchMock.mock.calls[0][0]).toBe(
      'https://api.example.invalid/gateway/api/log/self/export?'
    )
  } finally {
    vi.unstubAllGlobals()
  }
})
