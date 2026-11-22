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
import { toast } from 'sonner'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { createAppQueryClient } from '@/lib/query-client'

afterEach(() => vi.restoreAllMocks())

describe('query HTTP 500 navigation policy', () => {
  it.each([
    ['default', undefined, true],
    ['toast-only opt-out', { errorToast: false }, true],
    [
      'explicit navigation opt-out',
      { redirectOnInternalServerError: false },
      false,
    ],
    [
      'explicit navigation opt-in',
      { redirectOnInternalServerError: true },
      true,
    ],
  ] as const)(
    'keeps %s independent of toast ownership',
    async (_case, meta, shouldRedirect) => {
      const redirect = vi.fn()
      const notify = vi.spyOn(toast, 'error').mockReturnValue('test-error')
      const client = createAppQueryClient(redirect)
      const failure = {
        response: { status: 500, data: { message: 'Server failed' } },
      }
      try {
        await expect(
          client.fetchQuery({
            queryKey: ['policy', _case],
            queryFn: async () => {
              throw failure
            },
            retry: false,
            meta,
          })
        ).rejects.toBe(failure)
        expect(redirect).toHaveBeenCalledTimes(shouldRedirect ? 1 : 0)
        expect(notify).toHaveBeenCalledTimes(
          meta && 'errorToast' in meta && meta.errorToast === false ? 0 : 1
        )
      } finally {
        client.clear()
      }
    }
  )

  it.each([401, 403])(
    'preserves HTTP %i rejection and notification instead of swallowing it',
    async (status) => {
      const redirect = vi.fn()
      const notify = vi.spyOn(toast, 'error').mockReturnValue('test-error')
      const client = createAppQueryClient(redirect)
      const failure = {
        response: { status, data: { message: 'Access denied' } },
      }
      try {
        await expect(
          client.fetchQuery({
            queryKey: ['authorization', status],
            queryFn: async () => {
              throw failure
            },
            retry: false,
            meta: { redirectOnInternalServerError: false },
          })
        ).rejects.toBe(failure)
        expect(notify).toHaveBeenCalledTimes(1)
        expect(redirect).not.toHaveBeenCalled()
      } finally {
        client.clear()
      }
    }
  )

  it('does not let an optional query suppress an unrelated query HTTP 500', async () => {
    const redirect = vi.fn()
    vi.spyOn(toast, 'error').mockReturnValue('test-error')
    const client = createAppQueryClient(redirect)
    try {
      await client
        .fetchQuery({
          queryKey: ['optional'],
          queryFn: async () => {
            throw { response: { status: 500 } }
          },
          retry: false,
          meta: { redirectOnInternalServerError: false },
        })
        .catch(() => undefined)
      await client
        .fetchQuery({
          queryKey: ['core'],
          queryFn: async () => {
            throw { response: { status: 500 } }
          },
          retry: false,
        })
        .catch(() => undefined)
      expect(redirect).toHaveBeenCalledTimes(1)
    } finally {
      client.clear()
    }
  })
})
