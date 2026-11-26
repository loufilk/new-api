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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook, waitFor } from '@testing-library/react'
import type { PropsWithChildren } from 'react'
import { afterEach, expect, it, vi } from 'vitest'

import {
  useDashboardContentVisibility,
  useFAQ,
} from '@/features/dashboard/hooks/use-status-data'
import { useApiKeyAddresses } from '@/features/keys/hooks/use-api-key-addresses'
import { api } from '@/lib/api'
import { writeCachedStatus } from '@/lib/status-query'

import { useUpdateOption } from '../use-update-option'

afterEach(() => {
  localStorage.clear()
  vi.restoreAllMocks()
})

const oldUrl = 'https://old.example.test'
const newUrl = 'https://new.example.test'
const fallbackUrl = 'https://default.example.test'
const before = {
  server_address: fallbackUrl,
  api_info: [{ url: oldUrl, route: 'old', description: '' }],
  api_info_enabled: true,
  faq: [{ question: 'old question', answer: 'old answer' }],
  faq_enabled: true,
  uptime_kuma_enabled: true,
}

const cases = [
  {
    key: 'api_info',
    next: [{ url: newUrl, route: 'new', description: '' }],
    visible: newUrl,
  },
  { key: 'api_info_enabled', next: false, visible: fallbackUrl },
  {
    key: 'faq',
    next: [{ question: 'new question', answer: 'new answer' }],
    visible: 'new question',
  },
  { key: 'faq_enabled', next: false, visible: '' },
  { key: 'uptime_kuma_enabled', next: false, visible: false },
] as const

it.each(cases)(
  'saving console_setting.$key refreshes its mounted console consumer',
  async ({ key, next, visible }) => {
    let server = { ...before }
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    client.setQueryData(['status'], before)
    writeCachedStatus(before)
    const get = vi.spyOn(api, 'get').mockImplementation(async () => ({
      data: { success: true, data: server },
    }))
    vi.spyOn(api, 'put').mockImplementation(async () => {
      server = { ...server, [key]: next }
      return { data: { success: true } }
    })

    const wrapper = (props: PropsWithChildren) => (
      <QueryClientProvider client={client}>
        {props.children}
      </QueryClientProvider>
    )
    const view = renderHook(
      () => {
        const { addresses } = useApiKeyAddresses()
        const { items } = useFAQ()
        const { uptimeKuma } = useDashboardContentVisibility()
        return {
          update: useUpdateOption(),
          visible: key.startsWith('api_info')
            ? addresses[0]?.url
            : key.startsWith('faq')
              ? (items[0]?.question ?? '')
              : uptimeKuma,
        }
      },
      { wrapper }
    )

    try {
      expect(view.result.current.visible).not.toBe(visible)
      expect(get).not.toHaveBeenCalled()
      await act(() =>
        view.result.current.update.mutateAsync({
          key: 'console_setting.' + key,
          value: typeof next === 'boolean' ? next : JSON.stringify(next),
        })
      )
      await waitFor(() => expect(view.result.current.visible).toBe(visible))
      expect(get).toHaveBeenCalledTimes(1)
      expect(JSON.parse(localStorage.getItem('status')!)[key]).toEqual(next)
    } finally {
      view.unmount()
      client.clear()
    }
  }
)

it('does not mark public status stale for a group list fetched from the separate uptime endpoint', async () => {
  const client = new QueryClient()
  client.setQueryData(['status'], before)
  writeCachedStatus(before)
  vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
  const wrapper = (props: PropsWithChildren) => (
    <QueryClientProvider client={client}>{props.children}</QueryClientProvider>
  )
  const view = renderHook(useUpdateOption, { wrapper })
  try {
    await act(() =>
      view.result.current.mutateAsync({
        key: 'console_setting.uptime_kuma_groups',
        value: '[]',
      })
    )
    expect(client.getQueryState(['status'])?.isInvalidated).toBe(false)
    expect(JSON.parse(localStorage.getItem('status')!)).toEqual(before)
  } finally {
    view.unmount()
    client.clear()
  }
})
