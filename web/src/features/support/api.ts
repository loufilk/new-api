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
import { api, type ApiRequestConfig } from '@/lib/api'

import { SUPPORT_IMAGE_MAX_BYTES, SUPPORT_IMAGE_TYPES } from './lib/attachments'
import type {
  ApiResponse,
  SupportTicket,
  SupportTicketDetail,
  SupportTicketListData,
  SupportTicketStatus,
  SupportTicketSubmission,
  SupportTicketCreateSubmission,
} from './types'

const mutationConfig: ApiRequestConfig = {
  skipBusinessError: true,
  skipErrorHandler: true,
}

function requireSuccess<T>(response: ApiResponse<T>): T {
  if (!response.success) throw new Error(response.message)
  return response.data
}

function supportBasePath(admin: boolean): string {
  return admin ? '/api/admin/support/tickets' : '/api/support/tickets'
}

function supportPayload(
  input: SupportTicketCreateSubmission | SupportTicketSubmission
) {
  const { images, ...fields } = input
  if (!images?.length) {
    return Object.fromEntries(
      Object.entries(fields).filter(([, value]) => value !== undefined)
    )
  }
  const body = new FormData()
  for (const [key, value] of Object.entries(fields)) {
    if (value !== undefined) body.append(key, value)
  }
  for (const image of images) body.append('images', image)
  return body
}

export async function getSupportTicketAttachment(
  admin: boolean,
  ticketId: number,
  attachmentId: string,
  signal?: AbortSignal
): Promise<Blob> {
  const response = await api.get<Blob>(
    `${supportBasePath(admin)}/${ticketId}/attachments/${encodeURIComponent(attachmentId)}`,
    {
      responseType: 'blob',
      signal,
      disableDuplicate: true,
      skipErrorHandler: true,
    }
  )
  const blob = response.data
  if (
    !(blob instanceof Blob) ||
    !SUPPORT_IMAGE_TYPES.includes(blob.type) ||
    blob.size === 0 ||
    blob.size > SUPPORT_IMAGE_MAX_BYTES
  ) {
    throw new Error('Invalid support image response')
  }
  return blob
}

export async function listSupportTickets(params: {
  admin: boolean
  keyword?: string
  status?: SupportTicketStatus
  unreadOnly?: boolean
  page?: number
  pageSize?: number
}): Promise<SupportTicketListData> {
  const response = await api.get<ApiResponse<SupportTicketListData>>(
    supportBasePath(params.admin),
    {
      params: {
        keyword: params.keyword || undefined,
        status: params.status || undefined,
        unread: params.unreadOnly ? true : undefined,
        p: params.page ?? 1,
        page_size: params.pageSize ?? 20,
      },
    }
  )
  return requireSuccess(response.data)
}

export async function createSupportTicket(
  input: SupportTicketCreateSubmission
): Promise<SupportTicketDetail> {
  const response = await api.post<ApiResponse<SupportTicketDetail>>(
    '/api/support/tickets',
    supportPayload(input),
    mutationConfig
  )
  return requireSuccess(response.data)
}

export async function getSupportTicket(
  admin: boolean,
  ticketId: number
): Promise<SupportTicketDetail> {
  const response = await api.get<ApiResponse<SupportTicketDetail>>(
    `${supportBasePath(admin)}/${ticketId}`
  )
  return requireSuccess(response.data)
}

export async function replySupportTicket(
  input: {
    admin: boolean
    ticketId: number
  } & SupportTicketSubmission
): Promise<SupportTicket> {
  const response = await api.post<ApiResponse<SupportTicket>>(
    `${supportBasePath(input.admin)}/${input.ticketId}/replies`,
    supportPayload({
      message: input.message,
      request_id: input.request_id,
      images: input.images,
    }),
    mutationConfig
  )
  return requireSuccess(response.data)
}

export async function markSupportTicketRead(input: {
  admin: boolean
  ticketId: number
}): Promise<void> {
  const response = await api.post<ApiResponse<null>>(
    `${supportBasePath(input.admin)}/${input.ticketId}/read`,
    undefined,
    mutationConfig
  )
  requireSuccess(response.data)
}

export async function updateSupportTicketStatus(input: {
  ticketId: number
  status: SupportTicketStatus
}): Promise<SupportTicket> {
  const response = await api.put<ApiResponse<SupportTicket>>(
    `/api/admin/support/tickets/${input.ticketId}/status`,
    { status: input.status },
    mutationConfig
  )
  return requireSuccess(response.data)
}
