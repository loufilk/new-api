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
import type { QueryClient } from '@tanstack/react-query'

import { getServerErrorSources } from '@/lib/server-error-message'
import { statusQueryOptions } from '@/lib/status-query'

/** UI gate only: the API independently checks the database for every request. */
export async function verifyAccountPoolAccess(
  client: QueryClient
): Promise<boolean> {
  try {
    const status = await client.fetchQuery({
      ...statusQueryOptions,
      staleTime: 0,
      retry: false,
    })
    if (status?.account_pool_enabled === true) return true
  } catch {
    // Never fall back to a cached enable flag when verification fails.
  }
  await client.cancelQueries({ queryKey: ['account-pool', 'snapshot'] })
  client.removeQueries({ queryKey: ['account-pool', 'snapshot'] })
  return false
}

export function isAccountPoolDisabled(error: unknown): boolean {
  return getServerErrorSources(error).some(
    (source) => source.code === 'ACCOUNT_POOL_DISABLED'
  )
}
