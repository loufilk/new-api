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
import i18next from 'i18next'
import { useState, useEffect, useCallback, useRef } from 'react'
import { toast } from 'sonner'

import { handleServerError } from '@/lib/handle-server-error'
import {
  getServerMessage,
  requireServerSuccess,
} from '@/lib/server-error-message'

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
  const [affiliateCode, setAffiliateCode] = useState<string>('')
  const [affiliateLink, setAffiliateLink] = useState<string>('')
  const [loading, setLoading] = useState(true)
  const [transferring, setTransferring] = useState(false)
  const pendingTransferRef = useRef<{ quota: number; key: string } | null>(null)

  // Fetch affiliate code
  const fetchAffiliateCode = useCallback(async () => {
    try {
      setLoading(true)
      const response = requireServerSuccess(await getAffiliateCode())

      if (response.success && response.data) {
        setAffiliateCode(response.data)
        const link = generateAffiliateLink(response.data)
        setAffiliateLink(link)
      }
    } catch (error) {
      handleServerError(error)
    } finally {
      setLoading(false)
    }
  }, [])

  const transferQuota = useCallback(async (quota: number): Promise<boolean> => {
    if (!Number.isFinite(quota) || quota <= 0) {
      toast.error(i18next.t('Transfer failed'))
      return false
    }
    let idempotencyKey =
      pendingTransferRef.current?.quota === quota
        ? pendingTransferRef.current.key
        : ''
    if (!idempotencyKey) {
      idempotencyKey = createTransferIdempotencyKey()
      pendingTransferRef.current = { quota, key: idempotencyKey }
    }
    try {
      setTransferring(true)
      const response = await transferAffiliateQuota({ quota }, idempotencyKey)
      if (response.success) {
        pendingTransferRef.current = null
        toast.success(
          getServerMessage(response) || i18next.t('Transfer successful')
        )
        return true
      }
      handleServerError(response, i18next.t('Transfer failed'))
      return false
    } catch (_error) {
      handleServerError(_error, i18next.t('Transfer failed'))
      return false
    } finally {
      setTransferring(false)
    }
  }, [])

  useEffect(() => {
    fetchAffiliateCode()
  }, [fetchAffiliateCode])

  return {
    affiliateCode,
    affiliateLink,
    loading,
    transferring,
    transferQuota,
    refetch: fetchAffiliateCode,
  }
}
