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
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import {
  createSupportTicket,
  listSupportTickets,
  replySupportTicket,
  updateSupportTicketStatus,
} from '../api'

vi.mock('@/lib/api', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
  },
}))

const getMock = vi.mocked(api.get)
const postMock = vi.mocked(api.post)
const putMock = vi.mocked(api.put)

beforeEach(() => {
  getMock.mockReset()
  postMock.mockReset()
  putMock.mockReset()
})

describe('support ticket API keeps user and admin routes separate', () => {
  test('list sends search, status, unread, and pagination to the user endpoint', async () => {
    getMock.mockResolvedValue({
      data: {
        success: true,
        message: 'success',
        data: {
          items: [],
          total: 0,
          page: 2,
          page_size: 20,
          status_counts: { processing: 0, awaiting_user: 0, resolved: 0 },
        },
      },
    })

    await listSupportTickets({
      admin: false,
      keyword: 'image',
      status: 'processing',
      unreadOnly: true,
      page: 2,
      pageSize: 20,
    })

    expect(getMock).toHaveBeenCalledWith('/api/support/tickets', {
      params: {
        keyword: 'image',
        status: 'processing',
        unread: true,
        p: 2,
        page_size: 20,
      },
    })
  })

  test('admin reply and status update use admin-only endpoints', async () => {
    const ticket = {
      id: 9,
      user_id: 3,
      subject: 'Help',
      status: 'awaiting_user',
      user_unread: true,
      admin_unread: false,
      last_message_at: 1,
      created_at: 1,
      updated_at: 1,
    }
    postMock.mockResolvedValue({
      data: { success: true, message: 'success', data: ticket },
    })
    putMock.mockResolvedValue({
      data: {
        success: true,
        message: 'success',
        data: { ...ticket, status: 'resolved' },
      },
    })

    await replySupportTicket({ admin: true, ticketId: 9, message: 'Reply' })
    await updateSupportTicketStatus({ ticketId: 9, status: 'resolved' })

    expect(postMock).toHaveBeenCalledWith(
      '/api/admin/support/tickets/9/replies',
      { message: 'Reply' },
      expect.objectContaining({ skipBusinessError: true })
    )
    expect(putMock).toHaveBeenCalledWith(
      '/api/admin/support/tickets/9/status',
      { status: 'resolved' },
      expect.objectContaining({ skipBusinessError: true })
    )
  })

  test('business errors are surfaced instead of returning invalid data', async () => {
    postMock.mockResolvedValue({
      data: { success: false, message: 'invalid input', data: null },
    })

    await expect(
      createSupportTicket({ subject: '', message: '' })
    ).rejects.toThrow('invalid input')
  })
})
