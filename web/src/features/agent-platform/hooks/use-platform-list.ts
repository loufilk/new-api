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
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'

import { useAuthStore } from '@/stores/auth-store'

import { getPlatform } from '../api'
import type { PlatformPage } from '../types'

export function usePlatformList<T>(
  resource: string,
  filters: Record<string, string | number | undefined> = {},
  enabled = true
) {
  const user = useAuthStore((state) => state.auth.user)
  const [pagination, setPagination] = useState({ pageIndex: 0, pageSize: 20 })
  const query = useQuery({
    queryKey: [
      'agent-platform',
      user?.id,
      user?.role,
      user?.agent_profile?.version,
      resource,
      filters,
      pagination,
    ],
    queryFn: ({ signal }) =>
      getPlatform<PlatformPage<T>>(
        resource,
        {
          ...filters,
          p: pagination.pageIndex + 1,
          page_size: pagination.pageSize,
        },
        signal
      ),
    enabled: Boolean(user && enabled),
  })
  return { query, pagination, setPagination }
}
