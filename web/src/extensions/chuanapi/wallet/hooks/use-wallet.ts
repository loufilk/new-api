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

import { requireServerSuccess } from '@/lib/server-error-message'

import { getReferralRewards, getWalletSummary } from '../api'
import type { ReferralCommissionPage, WalletSummary } from '../types'

// ============================================================================
// Wallet / Referral Data Hook (TokenMix P1)
// ============================================================================

export function useWalletSummary() {
  const query = useQuery({
    queryKey: ['wallet', 'summary'],
    queryFn: async (): Promise<WalletSummary> => {
      const response = requireServerSuccess(await getWalletSummary())
      if (!response.data) throw new Error('Wallet summary is unavailable')
      return response.data
    },
  })

  return {
    summary: query.data ?? null,
    loading: query.isFetching,
    error: query.error,
    refetch: query.refetch,
  }
}

export function useReferralRewards(page: number, pageSize: number) {
  const query = useQuery({
    queryKey: ['wallet', 'referral-rewards', page, pageSize],
    queryFn: async (): Promise<ReferralCommissionPage> => {
      const response = requireServerSuccess(
        await getReferralRewards(page, pageSize)
      )
      if (!response.data) throw new Error('Referral rewards are unavailable')
      return response.data
    },
  })

  return {
    records: query.data?.items ?? [],
    total: query.data?.total ?? 0,
    loading: query.isFetching,
    error: query.error,
    refetch: query.refetch,
  }
}
