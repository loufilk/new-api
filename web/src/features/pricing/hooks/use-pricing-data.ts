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
import { useMemo } from 'react'

import { useStatus } from '@/hooks/use-status'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import { getPricing, getPublicPricing } from '../api'

export function usePricingData(enabled = true) {
  const { status } = useStatus()
  const bootstrapState = useAuthStore((state) => state.auth.bootstrapState)
  const userID = useAuthStore((state) => state.auth.user?.id ?? null)
  const sessionSID = useAuthStore((state) => state.auth.session?.sid ?? null)

  // The endpoint follows the resolved identity instead of a 401 downgrade:
  // anonymous visitors use the public catalogue, authenticated users keep the
  // dashboard endpoint (and its group pricing). `checking` means the identity
  // probe is still in flight, so wait for it rather than guessing.
  const identityResolved = bootstrapState !== 'checking'
  const isAuthenticated = identityResolved && userID !== null

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: [
      'pricing',
      isAuthenticated ? `user:${userID}:${sessionSID ?? ''}` : 'public',
    ],
    queryFn: async () =>
      requireServerSuccess(
        await (isAuthenticated ? getPricing() : getPublicPricing())
      ),
    staleTime: 5 * 60 * 1000,
    enabled: enabled && identityResolved,
  })

  // Ensure rates never reach zero to prevent division errors
  const priceRate = useMemo(
    () => Math.max((status?.price as number) ?? 1, 0.001),
    [status?.price]
  )
  const usdExchangeRate = useMemo(
    () => Math.max((status?.usd_exchange_rate as number) ?? priceRate, 0.001),
    [status?.usd_exchange_rate, priceRate]
  )

  const models = useMemo(() => {
    if (!data?.data || !data?.vendors) return []

    const vendorMap = new Map(data.vendors.map((v) => [v.id, v]))

    return data.data.map((model) => {
      const vendor = model.vendor_id
        ? vendorMap.get(model.vendor_id)
        : undefined
      return {
        ...model,
        key: model.model_name,
        vendor_name: vendor?.name,
        vendor_icon: vendor?.icon,
        vendor_description: vendor?.description,
        group_ratio: data.group_ratio,
      }
    })
  }, [data])

  return {
    models,
    vendors: data?.vendors ?? [],
    groupRatio: data?.group_ratio ?? {},
    usableGroup: data?.usable_group ?? {},
    endpointMap: data?.supported_endpoint ?? {},
    autoGroups: data?.auto_groups ?? [],
    isLoading,
    error,
    refetch,
    priceRate,
    usdExchangeRate,
  }
}
