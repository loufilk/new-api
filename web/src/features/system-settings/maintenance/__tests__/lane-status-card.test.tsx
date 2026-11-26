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
import { QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18n from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeAll, expect, it, vi } from 'vitest'

import { laneStatusTranslations } from '@/i18n/lane-status-translations'
import { api } from '@/lib/api'
import { createAppQueryClient } from '@/lib/query-client'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { LaneStatusCard } from '../lane-status-card'

const locale = i18n.createInstance()

beforeAll(async () => {
  await locale.init({
    lng: 'en',
    resources: { en: { translation: laneStatusTranslations.en } },
  })
})

afterEach(() => {
  cleanup()
  useAuthStore.getState().auth.reset()
})

function role(roleValue: number = ROLE.SUPER_ADMIN, id = 17) {
  useAuthStore
    .getState()
    .auth.setUser({ id, role: roleValue, username: 'fixture' })
}

function harness() {
  const onServerError = vi.fn()
  const client = createAppQueryClient(onServerError)
  const view = render(
    <QueryClientProvider client={client}>
      <I18nextProvider i18n={locale}>
        <LaneStatusCard />
      </I18nextProvider>
    </QueryClientProvider>
  )
  return { view, client, onServerError }
}

const standard = {
  success: true,
  data: {
    build_profile: 'ordinary',
    gateway_enabled: false,
    runtime_capable: false,
    observation_scope: 'current_instance',
  },
}
const lab = {
  success: true,
  data: {
    ...standard.data,
    build_profile: 'enterprise_lane_lab',
    gateway_enabled: true,
  },
}

it('neither fetches nor displays an operations snapshot for a regular admin', async () => {
  role(ROLE.ADMIN)
  const get = vi.spyOn(api, 'get')
  harness()
  await act(async () => undefined)
  expect(screen.queryByRole('region')).not.toBeInTheDocument()
  expect(get).not.toHaveBeenCalled()
})

it('distinguishes a running lab gateway from the production runtime gate, without any control writes', async () => {
  role()
  const get = vi.spyOn(api, 'get').mockResolvedValue({ data: lab })
  const post = vi.spyOn(api, 'post')
  const put = vi.spyOn(api, 'put')
  harness()
  expect(await screen.findByText('Isolated lab build')).toBeInTheDocument()
  expect(screen.getByText('Enabled')).toBeInTheDocument()
  expect(screen.getByText('Blocked')).toBeInTheDocument()
  expect(
    screen.getByText(/does not show fleet-wide rollout/)
  ).toBeInTheDocument()
  expect(get).toHaveBeenCalledExactlyOnceWith(
    '/api/performance/lane_status',
    expect.objectContaining({ signal: expect.any(AbortSignal) })
  )
  expect(screen.getAllByRole('button')).toHaveLength(1)
  expect(post).not.toHaveBeenCalled()
  expect(put).not.toHaveBeenCalled()
})

it('clears the displayed instance snapshot after a failed refresh, then recovers explicitly', async () => {
  role()
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValueOnce({ data: lab })
    .mockRejectedValueOnce({ isAxiosError: true, response: { status: 500 } })
    .mockResolvedValueOnce({ data: standard })
  const { onServerError } = harness()
  expect(await screen.findByText('Isolated lab build')).toBeInTheDocument()
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Refresh' }))
  expect(
    await screen.findByText(/current status is unknown/)
  ).toBeInTheDocument()
  expect(screen.queryByText('Isolated lab build')).not.toBeInTheDocument()
  expect(onServerError).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Refresh' }))
  expect(await screen.findByText('Standard build')).toBeInTheDocument()
  expect(screen.getByText('Disabled')).toBeInTheDocument()
  expect(get).toHaveBeenCalledTimes(3)
})

it.each([401, 403])(
  'does not fall back to an earlier snapshot after HTTP %i',
  async (status) => {
    role()
    const get = vi
      .spyOn(api, 'get')
      .mockResolvedValueOnce({ data: lab })
      .mockRejectedValueOnce({ isAxiosError: true, response: { status } })
    harness()
    await screen.findByText('Isolated lab build')
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Refresh' }))
    expect(
      await screen.findByText(/current status is unknown/)
    ).toBeInTheDocument()
    expect(screen.queryByText('Enabled')).not.toBeInTheDocument()
    expect(screen.queryByText('Isolated lab build')).not.toBeInTheDocument()
    expect(get).toHaveBeenCalledTimes(2)
  }
)

it.each([
  { success: false, message: 'denied' },
  { success: true, data: { ...standard.data, observation_scope: 'all' } },
  { success: true, data: { ...standard.data, gateway_enabled: true } },
  { success: true, data: { ...standard.data, runtime_capable: 'false' } },
])(
  'never turns a denial or malformed scope/state into an operational claim',
  async (data) => {
    role()
    vi.spyOn(api, 'get').mockResolvedValue({ data })
    harness()
    expect(
      await screen.findByText(/current status is unknown/)
    ).toBeInTheDocument()
    expect(screen.queryByText('Standard build')).not.toBeInTheDocument()
    expect(screen.queryByText('Enabled')).not.toBeInTheDocument()
  }
)

it('drops the prior snapshot on operator switch and aborts the next pending fetch on logout', async () => {
  role()
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValueOnce({ data: lab })
    .mockImplementationOnce(() => new Promise(() => undefined))
  harness()
  await screen.findByText('Isolated lab build')
  act(() => role(ROLE.SUPER_ADMIN, 22))
  await waitFor(() => expect(get).toHaveBeenCalledTimes(2))
  expect(screen.queryByText('Isolated lab build')).not.toBeInTheDocument()
  const signal = get.mock.calls[1][1]?.signal as AbortSignal
  act(() => useAuthStore.getState().auth.reset())
  expect(signal.aborted).toBe(true)
  expect(screen.queryByRole('region')).not.toBeInTheDocument()
})
