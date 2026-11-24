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
import {
  focusManager,
  QueryClient,
  QueryClientProvider,
} from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'

import { SupportCenter } from '@/features/support'
import type { SupportTicketDetail } from '@/features/support/types'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

const clients: QueryClient[] = []
const path = '/api/support/tickets'
const detailPath = `${path}/7`
const readPath = `${detailPath}/read-observed`

function getTicket(id = 7): SupportTicketDetail['ticket'] {
  return {
    id,
    user_id: 201,
    subject: `Support case ${id}`,
    status: 'processing',
    user_unread: false,
    admin_unread: false,
    last_message_at: 1,
    created_at: 1,
    updated_at: 1,
  }
}

function getConversationState() {
  const state: SupportTicketDetail = {
    ticket: getTicket(),
    messages: [
      {
        id: 11,
        ticket_id: 7,
        author_role: 'user',
        content: 'Original support request',
        created_at: 1,
      },
    ],
  }
  return state
}

function receiveReply(state: SupportTicketDetail, id = 12) {
  state.ticket.user_unread = true
  state.ticket.status = 'awaiting_user'
  state.ticket.last_message_at = id
  state.ticket.updated_at = id
  state.messages.push({
    id,
    ticket_id: state.ticket.id,
    author_role: 'admin',
    content: `Support reply ${id}`,
    created_at: id,
  })
}

function setup(
  state: SupportTicketDetail,
  options: {
    initialSelection?: boolean
    getDetail?: (
      url: string
    ) => SupportTicketDetail | Promise<SupportTicketDetail>
  } = {}
) {
  focusManager.setFocused(true)
  useAuthStore.getState().auth.setUser({ id: 201, username: 'user', role: 1 })
  const client = new QueryClient({
    defaultOptions: {
      queries: { gcTime: 0, staleTime: 10_000, refetchOnWindowFocus: false },
    },
  })
  clients.push(client)
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/support-email/preferences') {
      return {
        data: {
          success: true,
          data: {
            enabled: false,
            system_enabled: false,
            email_verified: false,
          },
        },
      }
    }
    if (url === path) {
      return {
        data: {
          success: true,
          data: {
            items: [structuredClone(state.ticket), getTicket(8)],
            total: 2,
            page: 1,
            page_size: 20,
            status_counts: { processing: 2, awaiting_user: 0, resolved: 0 },
          },
        },
      }
    }
    if (url === detailPath || url === `${path}/8`) {
      const detail = await options.getDetail?.(String(url))
      return {
        data: { success: true, data: structuredClone(detail ?? state) },
      }
    }
    throw new Error(`Unexpected GET: ${url}`)
  })
  const post = vi.spyOn(api, 'post').mockImplementation(async (url) => {
    if (url !== readPath) throw new Error(`Unexpected POST: ${url}`)
    state.ticket.user_unread = false
    return { data: { success: true, data: { acknowledged: true } } }
  })
  const rendered = render(
    <QueryClientProvider client={client}>
      <SupportCenter
        ticketId={options.initialSelection === false ? undefined : 7}
      />
    </QueryClientProvider>
  )
  return { get, post, rendered }
}

afterEach(() => {
  clients.splice(0).forEach((client) => client.clear())
  focusManager.setFocused(undefined)
  vi.useRealTimers()
  useAuthStore.getState().auth.reset()
})

test('refreshes a visible selected conversation at a bounded interval and pauses when the tab is hidden', async () => {
  vi.useFakeTimers()
  const state = getConversationState()
  const { get, post, rendered } = setup(state)
  try {
    const detailRequestCount = () =>
      get.mock.calls.filter(([url]) => url === detailPath).length
    await act(() => vi.advanceTimersByTimeAsync(25))
    expect(screen.getByText('Original support request')).toBeVisible()
    expect(detailRequestCount()).toBe(1)

    receiveReply(state)
    await act(() => vi.advanceTimersByTimeAsync(19_000))
    expect(screen.queryByText('Support reply 12')).not.toBeInTheDocument()
    await act(() => vi.advanceTimersByTimeAsync(1_500))
    expect(screen.getByText('Support reply 12')).toBeVisible()
    expect(post).toHaveBeenCalledWith(
      readPath,
      { through_message_id: 12, observed_status: 'awaiting_user' },
      expect.any(Object)
    )

    act(() => focusManager.setFocused(false))
    const countWhileHidden = get.mock.calls.length
    const writesWhileHidden = post.mock.calls.length
    receiveReply(state, 13)
    await act(() => vi.advanceTimersByTimeAsync(120_000))
    expect(get.mock.calls).toHaveLength(countWhileHidden)
    expect(post.mock.calls).toHaveLength(writesWhileHidden)

    act(() => focusManager.setFocused(true))
    await act(() => vi.advanceTimersByTimeAsync(25))
    expect(screen.getByText('Support reply 13')).toBeVisible()
  } finally {
    rendered.unmount()
  }
})

test('manual refresh preserves a draft and old scroll position without marking a reply read', async () => {
  const state = getConversationState()
  const { post } = setup(state)
  const input = await screen.findByLabelText('Reply')
  fireEvent.change(input, { target: { value: 'Unsaved reply draft' } })

  const conversation = screen.getByRole('log')
  Object.defineProperties(conversation, {
    scrollHeight: { configurable: true, value: 700 },
    clientHeight: { configurable: true, value: 200 },
  })
  conversation.scrollTop = 20
  fireEvent.scroll(conversation)
  receiveReply(state)
  fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
  expect(await screen.findByText('Support reply 12')).toBeVisible()
  expect(screen.getByLabelText('Reply')).toHaveValue('Unsaved reply draft')
  expect(conversation.scrollTop).toBe(20)
  expect(post).not.toHaveBeenCalled()

  conversation.scrollTop = 500
  fireEvent.scroll(conversation)
  await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
})

test('refreshes after a read conflict and only acknowledges the new rendered snapshot', async () => {
  const state = getConversationState()
  receiveReply(state)
  const { post } = setup(state)
  post.mockImplementationOnce(async () => {
    receiveReply(state, 13)
    return { data: { success: true, data: { acknowledged: false } } }
  })
  await screen.findByText('Support reply 13')
  await waitFor(() => expect(post).toHaveBeenCalledTimes(2))
  expect(post).toHaveBeenNthCalledWith(
    1,
    readPath,
    { through_message_id: 12, observed_status: 'awaiting_user' },
    expect.any(Object)
  )
  expect(post).toHaveBeenNthCalledWith(
    2,
    readPath,
    { through_message_id: 13, observed_status: 'awaiting_user' },
    expect.any(Object)
  )
})

test('a slow detail for the previously selected ticket is never marked read', async () => {
  const state = getConversationState()
  receiveReply(state)
  let resolvePending: (value: SupportTicketDetail) => void = () => {
    throw new Error('pending detail was not initialized')
  }
  const pending = new Promise<SupportTicketDetail>((resolve) => {
    resolvePending = resolve
  })
  const { get, post } = setup(state, {
    initialSelection: false,
    getDetail: (url) =>
      url === detailPath ? pending : { ticket: getTicket(8), messages: [] },
  })
  fireEvent.click(await screen.findByRole('button', { name: /Support case 7/ }))
  await waitFor(() =>
    expect(get).toHaveBeenCalledWith(detailPath, expect.any(Object))
  )
  fireEvent.click(screen.getByRole('button', { name: /Support case 8/ }))
  await screen.findByRole('log', { name: 'Ticket #8' })

  await act(async () => resolvePending(state))
  expect(screen.queryByText('Support reply 12')).not.toBeInTheDocument()
  expect(post).not.toHaveBeenCalled()
})
