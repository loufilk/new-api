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
import { useStatus } from '@/hooks/use-status'
import { API_BASE_URL } from '@/lib/api-url'

/** Share one resolved API base between snippets and endpoint copy actions. */
export function useMarketApiBase() {
  const { status } = useStatus()
  const candidate: unknown =
    status?.server_address ?? status?.data?.server_address
  const fallback = typeof window === 'undefined' ? '' : window.location.origin
  if (API_BASE_URL) return API_BASE_URL
  if (typeof candidate !== 'string') return fallback
  try {
    const url = new URL(candidate)
    if (
      !['https:', 'http:'].includes(url.protocol) ||
      url.username ||
      url.password ||
      url.search ||
      url.hash
    ) {
      return fallback
    }
    return url.href.replace(/\/+$/, '')
  } catch {
    return fallback
  }
}
