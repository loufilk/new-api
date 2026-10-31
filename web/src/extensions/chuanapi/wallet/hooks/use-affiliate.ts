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

import type { OperationDraft } from '@/features/agent-platform/types'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import { getAffiliateCode } from '../api'
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
  const [transferDraft, setTransferDraft] = useState<OperationDraft | null>(
    null
  )
  const transferResolver = useRef<((success: boolean) => void) | null>(null)
  const transferring = transferDraft !== null
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

  const transferQuota = useCallback((quota: number): Promise<boolean> => {
    if (
      transferResolver.current ||
      !Number.isSafeInteger(quota) ||
      quota <= 0
    ) {
      toast.error(i18next.t('Transfer failed'))
      return Promise.resolve(false)
    }
    if (pendingTransferRef.current?.quota !== quota) {
      pendingTransferRef.current = {
        quota,
        key: createTransferIdempotencyKey(),
      }
    }
    const requestId = pendingTransferRef.current.key
    return new Promise((resolve) => {
      transferResolver.current = resolve
      setTransferDraft({
        action: 'commission.convert.legacy',
        request_id: requestId,
        payload: { quota },
        endpoint: '/legacy-affiliate-transfer',
        resource: '/commissions/conversions',
      })
    })
  }, [])

  const finishTransfer = useCallback((success: boolean) => {
    const resolve = transferResolver.current
    transferResolver.current = null
    if (success) pendingTransferRef.current = null
    setTransferDraft(null)
    resolve?.(success)
  }, [])

  useEffect(
    () => () => {
      transferResolver.current?.(false)
      transferResolver.current = null
    },
    []
  )

  useEffect(() => {
    fetchAffiliateCode()
  }, [fetchAffiliateCode])

  return {
    affiliateCode,
    affiliateLink,
    loading,
    transferring,
    transferQuota,
    transferDraft,
    finishTransfer,
    refetch: fetchAffiliateCode,
  }
}
