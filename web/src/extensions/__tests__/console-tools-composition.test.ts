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
import { QueryClient } from '@tanstack/react-query'
import { expect, test, vi } from 'vitest'

import '@/extensions/bootstrap'
import { extensionConsoleTools } from '@/extensions'
import { api } from '@/lib/api'
import { STATUS_QUERY_KEY } from '@/lib/status-query'

test('the public console bridge preserves fresh account pool access checks', async () => {
  const tools = extensionConsoleTools()
  expect(tools.id).toBe('chuanapi/console-tools')
  expect(typeof tools.accountPoolPage).toBe('function')
  expect(typeof tools.accountPoolDiagnostics).toBe('function')
  expect(typeof tools.affiliatePage).toBe('function')
  expect(typeof tools.intelligencePage).toBe('function')
  const client = new QueryClient()
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { account_pool_enabled: true } },
  })
  expect(await tools.verifyAccountPoolAccess(client)).toBe(true)
  client.setQueryData(['account-pool', 'snapshot'], { items: ['previous'] })
  get.mockResolvedValue({
    data: { success: true, data: { account_pool_enabled: false } },
  })
  expect(await tools.verifyAccountPoolAccess(client)).toBe(false)
  expect(client.getQueryData(['account-pool', 'snapshot'])).toBeUndefined()
})

test('the public console bridge rejects access when fresh verification fails', async () => {
  const client = new QueryClient()
  client.setQueryData(STATUS_QUERY_KEY, { account_pool_enabled: true })
  vi.spyOn(api, 'get').mockRejectedValue(new Error('offline'))
  expect(await extensionConsoleTools().verifyAccountPoolAccess(client)).toBe(
    false
  )
})
