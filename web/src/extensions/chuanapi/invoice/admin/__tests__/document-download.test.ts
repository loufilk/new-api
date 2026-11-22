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
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { downloadInvoiceDocument } from '../../contracts'

beforeEach(() => {
  vi.stubGlobal(
    'URL',
    class extends URL {
      static createObjectURL = vi.fn(() => 'blob:synthetic')
      static revokeObjectURL = vi.fn()
    }
  )
})
afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

test.each([
  [true, '/api/admin/invoice/applications/17/document'],
  [false, '/api/invoice/applications/17/document'],
] as const)(
  'authorized download (admin=%s) uses only the private endpoint and revokes the local blob',
  async (admin, path) => {
    vi.useFakeTimers()
    const blob = new Blob(['test'], { type: 'application/pdf' })
    const get = vi.spyOn(api, 'get').mockResolvedValue({ data: blob })
    const click = vi
      .spyOn(HTMLAnchorElement.prototype, 'click')
      .mockImplementation(function (this: HTMLAnchorElement) {
        expect(this.download).toBe('invoice-17.pdf')
        expect(this.href).toBe('blob:synthetic')
      })
    await downloadInvoiceDocument(17, admin)
    expect(get).toHaveBeenCalledWith(
      path,
      expect.objectContaining({ responseType: 'blob' })
    )
    expect(URL.createObjectURL).toHaveBeenCalledWith(blob)
    expect(click).toHaveBeenCalledOnce()
    vi.runAllTimers()
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:synthetic')
  }
)

test('an error document is never exposed as a PDF download', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: new Blob(['{"success":false}'], { type: 'application/json' }),
  })
  await expect(downloadInvoiceDocument(17, true)).rejects.toThrow()
  expect(URL.createObjectURL).not.toHaveBeenCalled()
})

test('a response arriving after the reviewer closes the record cannot start a download', async () => {
  const controller = new AbortController()
  vi.spyOn(api, 'get').mockImplementation(async () => {
    controller.abort()
    return { data: new Blob(['test'], { type: 'application/pdf' }) }
  })
  await expect(
    downloadInvoiceDocument(17, true, controller.signal)
  ).rejects.toThrow()
  expect(URL.createObjectURL).not.toHaveBeenCalled()
})
