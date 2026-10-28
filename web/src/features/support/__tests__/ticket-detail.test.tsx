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
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, test, vi } from 'vitest'

import { TicketDetail } from '../components/ticket-detail'
import type { SupportTicketDetail } from '../types'

function detail(status: SupportTicketDetail['ticket']['status']) {
  return {
    ticket: {
      id: 7,
      user_id: 11,
      subject: 'Image request failed',
      status,
      user_unread: false,
      admin_unread: false,
      last_message_at: 1,
      created_at: 1,
      updated_at: 1,
    },
    messages: [
      {
        id: 1,
        ticket_id: 7,
        author_role: 'user' as const,
        content: 'Please help',
        created_at: 1,
      },
    ],
  }
}

describe('TicketDetail preserves user/admin conversation rules', () => {
  test('resolved user ticket is read-only and points the user to a new ticket', () => {
    render(
      <TicketDetail
        admin={false}
        detail={detail('resolved')}
        replyPending={false}
        statusPending={false}
        onReply={vi.fn()}
        onStatusChange={vi.fn()}
      />
    )

    expect(
      screen.getByText('This ticket has been resolved')
    ).toBeInTheDocument()
    expect(
      screen.getByText('If more help is needed, create a new ticket.')
    ).toBeInTheDocument()
    expect(screen.queryByLabelText('Reply')).not.toBeInTheDocument()
  })

  test('admin can reopen a resolved ticket and continue replying', async () => {
    const onStatusChange = vi.fn(async () => undefined)
    render(
      <TicketDetail
        admin
        detail={detail('resolved')}
        replyPending={false}
        statusPending={false}
        onReply={vi.fn(async () => undefined)}
        onStatusChange={onStatusChange}
      />
    )

    expect(screen.getByLabelText('Reply')).toBeInTheDocument()
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: 'Reopen ticket' }))
    expect(onStatusChange).toHaveBeenCalledWith('processing')
  })
})
