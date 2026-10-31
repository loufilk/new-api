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
import { render } from '@testing-library/react'
import type { AxiosRequestConfig } from 'axios'
import { vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { UserCallInterceptionSection } from '../section'
import type { CallInterceptionUpdate, UserCallInterception } from '../types'

export const config: UserCallInterception = {
  user_id: 12,
  enabled: false,
  reply: '',
  revision: 0,
  updated_at: 0,
  updated_by: 0,
}

export function setupRequests(saved = config) {
  const get = vi
    .spyOn(api, 'get')
    .mockImplementation(async (url: string, _options?: AxiosRequestConfig) => {
      if (url.endsWith('/hits') || url.endsWith('/audits')) {
        return {
          data: {
            success: true,
            data: { items: [], total: 0, page: 1, page_size: 10 },
          },
        }
      }
      return { data: { success: true, data: saved } }
    })
  const put = vi
    .spyOn(api, 'put')
    .mockImplementation(async (_url, payload) => ({
      data: {
        success: true,
        data: {
          ...saved,
          ...(payload as CallInterceptionUpdate),
          revision: saved.revision + 1,
          updated_by: 1,
        },
      },
    }))
  return { get, put }
}

export function renderInterception(
  options: {
    role?: number
    read?: boolean
    update?: boolean
    targetRole?: number
  } = {}
) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'operator',
    role: options.role ?? 100,
    permissions: {
      admin_permissions: {
        user_call_interception: {
          read: options.read ?? true,
          update: options.update ?? true,
        },
      },
    },
  })
  const view = render(
    <QueryClientProvider client={client}>
      <UserCallInterceptionSection
        userId={12}
        targetRole={options.targetRole ?? 1}
      />
    </QueryClientProvider>
  )
  return { ...view, client }
}
