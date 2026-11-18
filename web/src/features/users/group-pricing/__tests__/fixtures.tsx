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
import { vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { UserGroupPricingSection } from '../section'
import type { UserGroupPricing, UserGroupPricingUpdate } from '../types'

export const config: UserGroupPricing = {
  user_id: 12,
  revision: 1,
  ratios: { premium: 0.4 },
  updated_at: 100,
  updated_by: 1,
  groups: [
    {
      name: 'premium',
      description: 'Premium models',
      base_ratio: 0.6,
      effective_ratio: 0.4,
      available: true,
    },
    {
      name: 'default',
      description: 'Standard models',
      base_ratio: 1,
      effective_ratio: 1,
      available: true,
    },
    {
      name: 'private',
      description: 'Private models',
      base_ratio: 2,
      effective_ratio: 2,
      available: false,
    },
  ],
}

export function setupPricingRequests(initial = config) {
  let saved = structuredClone(initial)
  const get = vi
    .spyOn(api, 'get')
    .mockImplementation(async () => ({ data: { success: true, data: saved } }))
  const put = vi.spyOn(api, 'put').mockImplementation(async (_url, payload) => {
    const update = payload as UserGroupPricingUpdate
    saved = {
      ...saved,
      ratios: update.ratios,
      revision: saved.revision + 1,
      groups: saved.groups.map((group) => ({
        ...group,
        effective_ratio: Object.hasOwn(update.ratios, group.name)
          ? update.ratios[group.name]
          : group.base_ratio,
      })),
    }
    return { data: { success: true, data: saved } }
  })
  return { get, put }
}

export function renderPricing(
  options: {
    role?: number
    read?: boolean
    update?: boolean
    targetRole?: number
    userId?: number
  } = {}
) {
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'operator',
    role: options.role ?? 100,
    permissions: {
      admin_permissions: {
        user_group_pricing: {
          read: options.read ?? true,
          update: options.update ?? true,
        },
      },
    },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const view = (
    userId = options.userId ?? 12,
    targetRole = options.targetRole ?? 1
  ) => (
    <QueryClientProvider client={client}>
      <UserGroupPricingSection userId={userId} targetRole={targetRole} />
    </QueryClientProvider>
  )
  return { ...render(view()), client, view }
}
