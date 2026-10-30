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
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { buildSearchParams, logUserIdSearchSchema } from '../filter'
import { buildApiParams, fetchLogsByCategory } from '../utils'

afterEach(() => vi.restoreAllMocks())

describe('user ID log filter validation', () => {
  it.each(['1', '00042', '9007199254740993', '9223372036854775807'])(
    'preserves exact decimal ID %s without JavaScript number conversion',
    (userId) => {
      expect(logUserIdSearchSchema.parse(userId)).toBe(userId)
      const search = buildSearchParams({ userId, username: '123' }, 'common')
      const params = buildApiParams({
        page: 2,
        pageSize: 20,
        searchParams: search,
        isAdmin: true,
      })
      expect(params).toMatchObject({
        user_id: userId,
        username: '123',
        p: 2,
        page_size: 20,
      })
    }
  )

  it.each([
    '0',
    '000',
    '-1',
    '+1',
    '1.5',
    '1e3',
    '0x10',
    ' 42',
    '42 ',
    '１２',
    '9223372036854775808',
    '42 OR 1=1',
    ['42', '43'],
    9007199254740992,
  ])(
    'rejects invalid or ambiguous ID %j instead of dropping the filter',
    (userId) => {
      expect(logUserIdSearchSchema.safeParse(userId).success).toBe(false)
      expect(() =>
        buildApiParams({
          page: 1,
          pageSize: 20,
          searchParams: { userId },
          isAdmin: true,
        })
      ).toThrow('User ID must be a positive integer')
    }
  )

  it('accepts a safe numeric URL value but leaves an empty ID unfiltered', () => {
    expect(logUserIdSearchSchema.parse(42)).toBe('42')
    for (const userId of ['', undefined]) {
      const params = buildApiParams({
        page: 1,
        pageSize: 20,
        searchParams: { userId, username: '123' },
        isAdmin: true,
      })
      expect(params).not.toHaveProperty('user_id')
      expect(params.username).toBe('123')
    }
  })

  it('does not apply administrator IDs or usernames to personal log requests', () => {
    const params = buildApiParams({
      page: 1,
      pageSize: 20,
      searchParams: { userId: 'invalid', username: 'other', channel: '7' },
      isAdmin: false,
    })
    expect(params).not.toHaveProperty('user_id')
    expect(params).not.toHaveProperty('username')
    expect(params).not.toHaveProperty('channel')
  })

  it('fails closed before sending a log request when an ID is invalid', async () => {
    const get = vi.spyOn(api, 'get')
    await expect(
      fetchLogsByCategory({
        logCategory: 'common',
        isAdmin: true,
        page: 1,
        pageSize: 20,
        searchParams: { userId: 'invalid' },
        columnFilters: [],
      })
    ).rejects.toThrow('User ID must be a positive integer')
    expect(get).not.toHaveBeenCalled()
  })
})
