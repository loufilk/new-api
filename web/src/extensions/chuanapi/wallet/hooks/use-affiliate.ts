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
import i18next from 'i18next'
import { useState, useCallback, useRef } from 'react'
import { toast } from 'sonner'

import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import { getAffiliateCode, transferAffiliateQuota } from '../api'
import { generateAffiliateLink } from '../lib'

// ============================================================================
// Affiliate Hook
// ============================================================================

/**
 * Build a stable idempotency key for one transfer attempt. The backend treats
 * the same key + same amount as a replay, so a retry after a lost response
 * cannot debit the pending reward twice.
 */
function createTransferIdempotencyKey(): string {
  if (
    typeof crypto !== 'undefined' &&
    typeof crypto.randomUUID === 'function'
  ) {
    return crypto.randomUUID()
  }
  return `transfer-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`
}

export function useAffiliate() {
  const [transferring, setTransferring] = useState(false)
  const transferInFlight = useRef(false)
  const pendingTransferRef = useRef<{ quota: number; key: string } | null>(null)

  const invitation = useQuery({
    queryKey: ['wallet', 'affiliate-code'],
    queryFn: async () => {
      const response = requireServerSuccess(await getAffiliateCode())
      return response.data ?? ''
    },
  })
  const affiliateCode = invitation.data ?? ''
  const affiliateLink = affiliateCode
    ? generateAffiliateLink(affiliateCode)
    : ''

  const transferQuota = useCallback(async (quota: number): Promise<boolean> => {
    if (
      transferInFlight.current ||
      !Number.isSafeInteger(quota) ||
      quota <= 0
    ) {
      toast.error(i18next.t('Transfer failed'))
      return false
    }
    if (pendingTransferRef.current?.quota !== quota) {
      pendingTransferRef.current = {
        quota,
        key: createTransferIdempotencyKey(),
      }
    }
    transferInFlight.current = true
    setTransferring(true)
    try {
      requireServerSuccess(
        await transferAffiliateQuota({ quota }, pendingTransferRef.current.key)
      )
      pendingTransferRef.current = null
      return true
    } catch (error) {
      handleServerError(error)
      return false
    } finally {
      transferInFlight.current = false
      setTransferring(false)
    }
  }, [])

  return {
    affiliateCode,
    affiliateLink,
    loading: invitation.isPending,
    error: invitation.error,
    transferring,
    transferQuota,
    refetch: invitation.refetch,
  }
}
