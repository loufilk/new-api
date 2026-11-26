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
import { useCallback, useMemo, useRef, useState } from 'react'
import { toast } from 'sonner'

import {
  isWalletTransferRejected,
  transferReferralToAgent,
} from '@/features/agent-platform/simple-api'
import {
  createWalletTransferStore,
  walletTransferVersion,
  type WalletTransferIntent,
} from '@/features/agent-platform/stores/wallet-transfer-store'
import { getAuthSessionEpoch } from '@/lib/auth-session'
import { handleServerError } from '@/lib/handle-server-error'
import {
  createServerError,
  getServerErrorSources,
  requireServerSuccess,
} from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import { getAffiliateCode, transferAffiliateQuota } from '../api'
import { generateAffiliateLink } from '../lib'

// Separate from agent-wallet-transfer: identical user and amount do not
// identify the same operation when the source or destination is different.
const receipts = createWalletTransferStore('referral-transfer').getState()
export function useAffiliate() {
  const owner = useAuthStore((s) => s.auth.user?.id)
  const scope = useMemo(() => {
    try {
      return { owner, initial: owner ? receipts.read(owner) : null }
    } catch {
      // execute() verifies storage under the Web Lock and fails closed.
      return { owner, initial: null }
    }
  }, [owner])
  const [updated, setUpdated] = useState<{
    scope: typeof scope
    value: WalletTransferIntent | null
  } | null>(null)
  const snapshot = updated?.scope === scope ? updated.value : scope.initial
  const pendingTransfer = snapshot?.status === 'pending' ? snapshot : null
  const [transferring, setTransferring] = useState(false)
  const inFlight = useRef(false)
  const invitation = useQuery({
    queryKey: ['wallet', 'affiliate-code', owner],
    meta: { redirectOnInternalServerError: false },
    queryFn: async () => {
      const response = requireServerSuccess(await getAffiliateCode())
      return response.data ?? ''
    },
  })
  const affiliateCode = invitation.data ?? ''
  const affiliateLink = affiliateCode
    ? generateAffiliateLink(affiliateCode)
    : ''

  const transferQuota = useCallback(
    async (
      quota: number,
      target: 'wallet' | 'agent' = 'wallet'
    ): Promise<boolean> => {
      if (
        !owner ||
        inFlight.current ||
        !Number.isSafeInteger(quota) ||
        quota <= 0
      ) {
        toast.error(i18next.t('Transfer failed'))
        return false
      }
      inFlight.current = true
      setTransferring(true)
      const epoch = getAuthSessionEpoch()
      const current = () =>
        epoch === getAuthSessionEpoch() &&
        useAuthStore.getState().auth.user?.id === owner
      try {
        const done = await receipts.execute({
          userId: owner,
          version: walletTransferVersion(snapshot),
          draft: { quota, amount: String(quota), target },
          current,
          dispatch: async (intent) => {
            setUpdated({ scope, value: intent })
            if (target === 'wallet') {
              const result = await transferAffiliateQuota(
                { quota, expected_user_id: owner },
                intent.request_id
              )
              if (result?.success !== true) throw createServerError(result)
            } else {
              await transferReferralToAgent({
                quota,
                request_id: intent.request_id,
                expected_user_id: owner,
              })
            }
          },
          isDefiniteRejection: (error) =>
            target === 'wallet'
              ? getServerErrorSources(error).some(
                  (source) =>
                    source.success === false &&
                    source.code === 'REFERRAL_TRANSFER_INSUFFICIENT'
                )
              : isWalletTransferRejected(error),
        })
        return done && current()
      } catch (error) {
        if (current()) handleServerError(error)
        return false
      } finally {
        inFlight.current = false
        setTransferring(false)
        if (current()) {
          try {
            setUpdated({ scope, value: receipts.read(owner) })
          } catch {
            /* Keep the last identity; never create another on a storage failure. */
          }
        }
      }
    },
    [owner, scope, snapshot]
  )
  return {
    affiliateCode,
    affiliateLink,
    loading: invitation.isPending,
    error: invitation.error,
    transferring,
    pendingTransfer,
    transferQuota,
    refetch: invitation.refetch,
  }
}
