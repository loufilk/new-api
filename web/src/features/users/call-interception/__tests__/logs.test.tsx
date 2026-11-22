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
  cleanup,
  fireEvent,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'

import { config, renderInterception, setupRequests } from './fixtures'

const hit = {
  id: 1,
  time: 1793458800,
  user_id: 12,
  token_id: 41,
  request_id: 'request-safe-1',
  identity_kind: 'api_key',
  actor_user_id: 12,
  interface: '/v1/chat/completions',
  protocol: 'openai_chat',
  status: 200,
  revision: 7,
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})

it('shows independent hit metadata and paginates using the same user scope', async () => {
  const { get } = setupRequests()
  get.mockImplementation(async (url, options) => {
    if (url.endsWith('/hits')) {
      return {
        data: {
          success: true,
          data: {
            items: [
              {
                ...hit,
                request_id:
                  (options?.params as { p?: number } | undefined)?.p === 2
                    ? 'request-safe-2'
                    : hit.request_id,
                token: 'must-not-render-token',
                reply: 'must-not-render-reply',
                body: 'must-not-render-body',
              },
            ],
            total: 11,
            page: (options?.params as { p?: number } | undefined)?.p,
            page_size: 10,
          },
        },
      }
    }
    return { data: { success: true, data: config } }
  })
  renderInterception()
  await screen.findByRole('textbox', { name: 'Custom reply' })
  await userEvent.click(screen.getByRole('tab', { name: 'Interception hits' }))
  expect(await screen.findByText('request-safe-1')).toBeInTheDocument()
  expect(screen.getByText('/v1/chat/completions')).toBeInTheDocument()
  expect(screen.getByText('openai_chat')).toBeInTheDocument()
  expect(screen.queryByText(/must-not-render/)).not.toBeInTheDocument()
  expect(
    screen.getByRole('region', { name: 'Interception logs' })
  ).toHaveAttribute('tabindex', '0')
  await userEvent.click(screen.getByRole('button', { name: 'Go to next page' }))
  await screen.findByText('request-safe-2')
  expect(get).toHaveBeenLastCalledWith(
    '/api/user/12/call-interception/hits',
    expect.objectContaining({
      params: expect.objectContaining({ p: 2, page_size: 10 }),
    })
  )
})

it('applies key/request/time filters on Enter without submitting its surrounding user form and resets them', async () => {
  const { get, put } = setupRequests()
  renderInterception()
  await screen.findByRole('textbox', { name: 'Custom reply' })
  await userEvent.click(screen.getByRole('tab', { name: 'Interception hits' }))
  await screen.findByText('No interception records')
  await userEvent.type(
    screen.getByRole('textbox', { name: 'Request ID' }),
    'request-44'
  )
  await userEvent.type(screen.getByRole('textbox', { name: 'Key ID' }), '44')
  fireEvent.change(screen.getByLabelText('Start time'), {
    target: { value: '2026-10-01T00:00' },
  })
  fireEvent.change(screen.getByLabelText('End time'), {
    target: { value: '2026-10-31T23:59' },
  })
  await userEvent.type(
    screen.getByRole('textbox', { name: 'Request ID' }),
    '{Enter}'
  )
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith(
      '/api/user/12/call-interception/hits',
      expect.objectContaining({
        params: expect.objectContaining({
          p: 1,
          token_id: 44,
          request_id: 'request-44',
          start_time: expect.any(Number),
          end_time: expect.any(Number),
        }),
      })
    )
  )
  expect(put).not.toHaveBeenCalled()
  await userEvent.click(screen.getByRole('button', { name: 'Reset filters' }))
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith(
      '/api/user/12/call-interception/hits',
      expect.objectContaining({ params: { p: 1, page_size: 10 } })
    )
  )
})

it('rejects invalid IDs and reversed time ranges without issuing a filtered query', async () => {
  const { get } = setupRequests()
  renderInterception()
  await userEvent.click(screen.getByRole('tab', { name: 'Interception hits' }))
  await screen.findByText('No interception records')
  const initialCalls = get.mock.calls.length
  await userEvent.type(screen.getByRole('textbox', { name: 'Key ID' }), '-1')
  await userEvent.click(screen.getByRole('button', { name: 'Apply filters' }))
  expect(screen.getByRole('alert')).toHaveTextContent(
    'Enter a valid ID and time range'
  )
  expect(get).toHaveBeenCalledTimes(initialCalls)
  await userEvent.clear(screen.getByRole('textbox', { name: 'Key ID' }))
  fireEvent.change(screen.getByLabelText('Start time'), {
    target: { value: '2026-10-31T12:00' },
  })
  fireEvent.change(screen.getByLabelText('End time'), {
    target: { value: '2026-10-01T12:00' },
  })
  await userEvent.click(screen.getByRole('button', { name: 'Apply filters' }))
  expect(get).toHaveBeenCalledTimes(initialCalls)
})

it('preserves an unsaved reply across log tabs and renders audits without custom reply contents', async () => {
  const { get } = setupRequests()
  get.mockImplementation(async (url) => {
    if (url.endsWith('/audits')) {
      return {
        data: {
          success: true,
          data: {
            items: [
              {
                id: 1,
                time: 1793458800,
                user_id: 12,
                actor_id: 99,
                request_id: 'audit-safe',
                previous_enabled: false,
                enabled: true,
                reply_changed: true,
                revision: 7,
              },
            ],
            total: 1,
            page: 1,
            page_size: 10,
          },
        },
      }
    }
    return { data: { success: true, data: config } }
  })
  renderInterception()
  const reply = await screen.findByRole('textbox', { name: 'Custom reply' })
  fireEvent.change(reply, { target: { value: 'Unsaved draft' } })
  await userEvent.click(
    screen.getByRole('tab', { name: 'Configuration audit' })
  )
  await screen.findByText('audit-safe')
  expect(screen.getByText('Reply changed')).toBeInTheDocument()
  expect(screen.getByText('Disabled → Enabled')).toBeInTheDocument()
  expect(
    screen.getByRole('textbox', { name: 'Administrator ID' })
  ).toBeInTheDocument()
  await userEvent.click(screen.getByRole('tab', { name: 'Configuration' }))
  expect(screen.getByRole('textbox', { name: 'Custom reply' })).toHaveValue(
    'Unsaved draft'
  )
})

it('shows query failure rather than an empty log and retries on demand', async () => {
  const { get } = setupRequests()
  get.mockImplementation(async (url) => {
    if (url.endsWith('/hits')) throw new Error('Database unavailable')
    return { data: { success: true, data: config } }
  })
  renderInterception()
  await userEvent.click(screen.getByRole('tab', { name: 'Interception hits' }))
  await screen.findByText('Failed to load interception logs')
  expect(screen.queryByText('No interception records')).not.toBeInTheDocument()
  get.mockResolvedValue({
    data: {
      success: true,
      data: { items: [hit], total: 1, page: 1, page_size: 10 },
    },
  })
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  await screen.findByText('request-safe-1')
  const region = screen.getByRole('region', { name: 'Interception logs' })
  expect(within(region).getByText('200')).toBeInTheDocument()
})

it('distinguishes unauthenticated resource access from a call by the intercepted user and filters by identity', async () => {
  const { get } = setupRequests()
  get.mockImplementation(async (url) => {
    if (url.endsWith('/hits')) {
      return {
        data: {
          success: true,
          data: {
            items: [
              {
                ...hit,
                token_id: 0,
                actor_user_id: 0,
                identity_kind: 'public_artifact',
                request_id: 'resource-access',
              },
            ],
            total: 1,
            page: 1,
            page_size: 10,
          },
        },
      }
    }
    return { data: { success: true, data: config } }
  })
  renderInterception()
  await userEvent.click(screen.getByRole('tab', { name: 'Interception hits' }))
  await screen.findByText('resource-access')
  expect(
    screen.getByText('Resource access does not prove a call by this user.')
  ).toBeInTheDocument()
  expect(screen.queryByText('Caller user ID: 12')).not.toBeInTheDocument()
  await userEvent.click(
    screen.getByRole('combobox', { name: 'Request identity' })
  )
  await userEvent.click(
    screen.getByRole('option', { name: 'Public artifact access' })
  )
  await userEvent.type(
    screen.getByRole('textbox', { name: 'Caller user ID' }),
    '0'
  )
  await userEvent.click(screen.getByRole('button', { name: 'Apply filters' }))
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith(
      '/api/user/12/call-interception/hits',
      expect.objectContaining({
        params: expect.objectContaining({
          identity_kind: 'public_artifact',
          actor_user_id: 0,
        }),
      })
    )
  )
})

it('shows the actual administrator actor when viewing another user’s artifact', async () => {
  const { get } = setupRequests()
  get.mockImplementation(async (url) => {
    if (url.endsWith('/hits')) {
      return {
        data: {
          success: true,
          data: {
            items: [
              {
                ...hit,
                token_id: 0,
                actor_user_id: 99,
                identity_kind: 'dashboard_session',
                request_id: 'admin-artifact-read',
              },
            ],
            total: 1,
            page: 1,
            page_size: 10,
          },
        },
      }
    }
    return { data: { success: true, data: config } }
  })
  renderInterception()
  await userEvent.click(screen.getByRole('tab', { name: 'Interception hits' }))
  await screen.findByText('admin-artifact-read')
  expect(screen.getByText(/^User ID: 12/)).toBeInTheDocument()
  expect(screen.getByText('Caller user ID: 99')).toBeInTheDocument()
  expect(screen.queryByText('Caller user ID: 12')).not.toBeInTheDocument()
})

it('lets keyboard users open the identity selector without applying filters prematurely', async () => {
  const { get } = setupRequests()
  renderInterception()
  await userEvent.click(screen.getByRole('tab', { name: 'Interception hits' }))
  await screen.findByText('No interception records')
  const calls = get.mock.calls.length
  screen.getByRole('combobox', { name: 'Request identity' }).focus()
  await userEvent.keyboard('{Enter}')
  expect(
    await screen.findByRole('option', { name: 'API key' })
  ).toBeInTheDocument()
  expect(get).toHaveBeenCalledTimes(calls)
  await userEvent.keyboard('{Escape}')
  expect(
    screen.getByRole('combobox', { name: 'Request identity' })
  ).toHaveAttribute('aria-expanded', 'false')
})
