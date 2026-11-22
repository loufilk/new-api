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
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { fetchLogsByCategory, LOG_SUMMARY_FRESH_MS } from '../utils'

const client = new QueryClient()
afterEach(() => {
  client.clear()
  vi.useRealTimers()
  vi.restoreAllMocks()
})

function page(p: number, scope = 'self-account-seven') {
  return fetchLogsByCategory(
    {
      logCategory: 'common',
      isAdmin: false,
      page: p,
      pageSize: 1,
      searchParams: {},
      columnFilters: [],
    },
    { client, scope }
  )
}

function serve(logCount: () => number) {
  return vi.spyOn(api, 'get').mockImplementation(async (url) => {
    const params = new URL(url, 'http://localhost').searchParams
    const hasRow = Number(params.get('p')) <= logCount()
    return {
      data: {
        success: true,
        data: {
          items: hasRow ? [{ id: 1 }] : [],
          total: params.get('skip_total') === '1' ? -1 : logCount(),
        },
      },
    }
  })
}

it('counts a deep link and reuses only a fresh count with its exact time window', async () => {
  vi.useFakeTimers()
  let total = 7
  const get = serve(() => total)
  await page(2)
  vi.advanceTimersByTime(2000)
  expect((await page(3)).data?.total).toBe(7)
  const params = get.mock.calls.map(
    ([url]) => new URL(url, 'http://localhost').searchParams
  )
  expect(params[0].has('skip_total')).toBe(false)
  expect(params[1].get('skip_total')).toBe('1')
  expect(params[1].get('end_timestamp')).toBe(params[0].get('end_timestamp'))
  total = 10
  vi.advanceTimersByTime(LOG_SUMMARY_FRESH_MS)
  expect((await page(4)).data?.total).toBe(10)
  expect(get.mock.calls.at(-1)?.[0]).not.toContain('skip_total')
})

it('never borrows totals from another viewer or filter, or after an explicit first-page refresh', async () => {
  let total = 12
  const get = serve(() => total)
  await page(1)
  total = 4
  expect((await page(2, 'self-account-eight')).data?.total).toBe(4)
  total = 9
  expect((await page(1)).data?.total).toBe(9)
  expect(get.mock.calls.some(([url]) => url.includes('skip_total'))).toBe(false)
})

it('recounts when retention deletes a page that a fresh sample expected to exist', async () => {
  let total = 3
  const get = serve(() => total)
  await page(1)
  total = 2
  expect((await page(3)).data?.total).toBe(2)
  expect(get).toHaveBeenCalledTimes(3)
  expect(get.mock.calls[1][0]).toContain('skip_total=1')
  expect(get.mock.calls[2][0]).not.toContain('skip_total')
})
