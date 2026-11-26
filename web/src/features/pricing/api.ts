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
import { api } from '@/lib/api'
import { apiUrl } from '@/lib/api-url'

import type { PricingData, PublishedModelLinesResponse } from './types'

// ----------------------------------------------------------------------------
// Pricing APIs
// ----------------------------------------------------------------------------

// Get model pricing data for the authenticated dashboard API. This endpoint
// stays behind UserAuth and returns the caller's group ratios.
export async function getPricing(signal?: AbortSignal): Promise<PricingData> {
  // Let React Query own this request so an admin save can cancel a pending
  // pre-save read before refetching the committed projection.
  const res = await api.get('/api/pricing', { signal, disableDuplicate: true })
  return res.data
}

// Get the anonymous public catalogue. Public pages must use this endpoint
// instead of downgrading after an authentication failure. The request is
// deliberately credential-free: `withCredentials: false` keeps cookies off the
// wire, `preserveAuthorization` stops the request interceptor from attaching a
// session bearer token, and `skipAuthRefresh` prevents a 401 from starting a
// refresh cycle for a visitor that never authenticated.
export async function getPublicPricing(): Promise<PricingData> {
  const res = await api.get('/public-api/pricing', {
    withCredentials: false,
    preserveAuthorization: true,
    skipAuthRefresh: true,
  })
  return res.data
}

// Keep named lines out of the anonymously cacheable pricing response.
export async function getPublishedModelLines(signal?: AbortSignal): Promise<PublishedModelLinesResponse> {
  const response = await fetch(apiUrl('/public-api/model-lines'), {
    signal,
    credentials: 'omit',
    redirect: 'error',
    cache: 'no-store',
  })
  if (!response.ok) throw new Error('Public model lines are temporarily unavailable')
  return response.json()
}
