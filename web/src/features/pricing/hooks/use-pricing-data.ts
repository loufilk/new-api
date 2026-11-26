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

import { getPricing, getPublicPricing, getPublishedModelLines } from '../api'
import { mergePublishedModelLines } from '../lib/published-model-lines'
import { projectMarketActivity } from '../lib/group-market-activity'
import { publicTierCatalog, publicModelBrand } from '../lib/market-facets'

// Project private per-group details onto this model's already authorized groups.
// Absence is meaningful (public response or no configured value), not zero.
function modelGroupDetails<T>(
  values: Record<string, T> | undefined,
  groups: readonly string[],
  allowed: Record<string, unknown> | undefined
): Record<string, T> | undefined {
  if (!values || !allowed) return undefined
  return Object.fromEntries(
    groups
      .filter(
        (group) => Object.hasOwn(allowed, group) && Object.hasOwn(values, group)
      )
      .map((group) => [group, values[group]])
  )
}

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
    queryFn: async ({ signal }) =>
      requireServerSuccess(
        await (isAuthenticated ? getPricing(signal) : getPublicPricing())
      ),
    staleTime: 5 * 60 * 1000,
    // A return to the signed-in marketplace picks up another admin's
    // committed change without polling, even during the normal fresh interval.
    ...(isAuthenticated ? { refetchOnWindowFocus: 'always' as const } : {}),
    enabled: enabled && identityResolved,
  })

  // Revocable names never ride in the edge-cached base catalogue. Hide them on
  // an overlay failure rather than using React Query's previously loaded data.
  const linesQuery = useQuery({
    queryKey: ['public-model-lines'],
    enabled: enabled && identityResolved && !isAuthenticated,
    queryFn: async ({ signal }) =>
      requireServerSuccess(await getPublishedModelLines(signal)),
    refetchOnMount: 'always',
    refetchOnWindowFocus: 'always',
    refetchInterval: 15_000,
    gcTime: 0,
    retry: false,
  })
  const publicLines = linesQuery.isError ? undefined : linesQuery.data

  // Ensure rates never reach zero to prevent division errors
  const priceRate = useMemo(
    () => Math.max((status?.price as number) ?? 1, 0.001),
    [status?.price]
  )
  const usdExchangeRate = useMemo(
    () => Math.max((status?.usd_exchange_rate as number) ?? priceRate, 0.001),
    [status?.usd_exchange_rate, priceRate]
  )

  const groupMarketActivity = useMemo(
    () =>
      isAuthenticated && !data?.group_keys_are_display_only
        ? projectMarketActivity(data)
        : undefined,
    [data, isAuthenticated]
  )

  const displayTiers =
    !isAuthenticated || data?.group_keys_are_display_only === true
  const catalog = useMemo(() => {
    if (!data?.data) return { models: [], groupCatalog: undefined, groupRatio: {}, usableGroup: {} }

    const displayData = displayTiers ? mergePublishedModelLines(data, publicLines) : data

    const vendorMap = new Map((data.vendors ?? []).map((v) => [v.id, v]))

    const rows = displayData.data.map((model) => {
      const vendor = model.vendor_id
        ? vendorMap.get(model.vendor_id)
        : undefined
      return {
        ...model,
        key: model.model_name,
        vendor_name: vendor?.name,
        vendor_icon: vendor?.icon,
        vendor_description: vendor?.description,
        group_ratio: displayData.group_ratio,
        group_market_activity: modelGroupDetails(
          groupMarketActivity,
          model.enable_groups ?? [],
          data.usable_group
        ),
        group_rate_source: isAuthenticated
          ? modelGroupDetails(
              data.group_rate_source,
              model.enable_groups ?? [],
              data.usable_group
            )
          : undefined,
        group_rate_limits: isAuthenticated
          ? modelGroupDetails(
              data.group_rate_limits,
              model.enable_groups ?? [],
              data.usable_group
            )
          : undefined,
        group_keys_are_display_only:
          !isAuthenticated || data.group_keys_are_display_only,
      }
    })
    const groupCatalog = displayTiers
      ? {
          ...publicTierCatalog(rows, data.group_ratio ?? {}),
          ...(displayData !== data ? displayData.group_catalog : undefined),
        }
      : data.group_catalog
    const models = rows.map((row) => ({
      ...row,
      group_catalog: groupCatalog,
      vendor_icon:
        row.vendor_icon ||
        (displayTiers ? publicModelBrand(row).icon || undefined : undefined),
    }))
    return { models, groupCatalog, groupRatio: displayData.group_ratio, usableGroup: displayData.usable_group }
  }, [data, isAuthenticated, groupMarketActivity, displayTiers, publicLines])

  return {
    models: catalog.models,
    displayTiers,
    vendors: data?.vendors ?? [],
    groupRatio: catalog.groupRatio ?? {},
    usableGroup: catalog.usableGroup ?? {},
    endpointMap: data?.supported_endpoint ?? {},
    autoGroups: data?.auto_groups ?? [],
    modelOrder: data?.model_order,
    isLoading: isLoading || !identityResolved,
    groupCatalog: catalog.groupCatalog,
    groupMarketActivity,
    scope: isAuthenticated ? `user:${userID}:${sessionSID ?? ''}` : 'public',
    error,
    refetch,
    priceRate,
    usdExchangeRate,
  }
}
