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
export type SupportTicketStatus = 'processing' | 'awaiting_user' | 'resolved'

export interface SupportTicket {
  id: number
  user_id: number
  subject: string
  status: SupportTicketStatus
  user_unread: boolean
  admin_unread: boolean
  last_message_at: number
  created_at: number
  updated_at: number
}

export interface SupportTicketMessage {
  id: number
  ticket_id: number
  author_role: 'user' | 'admin'
  content: string
  created_at: number
}

export interface SupportTicketStatusCounts {
  processing: number
  awaiting_user: number
  resolved: number
}

export interface SupportTicketListData {
  items: SupportTicket[]
  total: number
  page: number
  page_size: number
  status_counts: SupportTicketStatusCounts
}

export interface SupportTicketDetail {
  ticket: SupportTicket
  messages: SupportTicketMessage[]
}

export interface ApiResponse<T> {
  success: boolean
  message: string
  data: T
}
