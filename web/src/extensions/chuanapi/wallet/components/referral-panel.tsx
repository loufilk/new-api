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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { postPlatform } from '@/features/agent-platform/api'
import { WithdrawalContactDialog } from '@/features/agent-platform/components/withdrawal-contact-dialog'
import { useRequestId } from '@/features/agent-platform/hooks/use-request-id'
import { getSimpleAgentBalance } from '@/features/agent-platform/simple-api'
import { getSelf } from '@/lib/api'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import { REFERRAL_RECORDS_PAGE_SIZE } from '../constants'
import {
  useAffiliate,
  useReferralRewards,
  useTopupInfo,
  useWalletSummary,
} from '../hooks'
import type { UserWalletData } from '../types'
import { AffiliateRewardsCard } from './affiliate-rewards-card'
import { TransferDialog } from './dialogs/transfer-dialog'

export function ReferralPanel() {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [page, setPage] = useState(1)
  const [transferOpen, setTransferOpen] = useState(false)
  const [withdrawalOpen, setWithdrawalOpen] = useState(false)
  const [agentTransferring, setAgentTransferring] = useState(false)
  const identify = useRequestId()
  const rewards = useReferralRewards(page, REFERRAL_RECORDS_PAGE_SIZE)
  const affiliate = useAffiliate()
  const wallet = useWalletSummary()
  const info = useTopupInfo()
  const user = useQuery({
    queryKey: ['wallet', 'user'],
    queryFn: async () =>
      requireServerSuccess(await getSelf()).data as UserWalletData,
  })
  const isAgent = !!user.data?.agent_profile && (user.data?.role ?? 1) < 10
  const balance = useQuery({
    queryKey: ['agent-platform', user.data?.id, 'simple-balance'],
    queryFn: ({ signal }) => getSimpleAgentBalance(signal),
    enabled: isAgent,
  })
  if (user.isError || wallet.error) {
    return (
      <ErrorState
        onRetry={() => {
          void user.refetch()
          void wallet.refetch()
        }}
      />
    )
  }
  return (
    <>
      <AffiliateRewardsCard
        user={user.data ?? null}
        affiliateLink={affiliate.affiliateLink}
        onTransfer={() => setTransferOpen(true)}
        onWithdraw={() => setWithdrawalOpen(true)}
        availableQuota={wallet.summary?.referral_available_quota ?? 0}
        complianceConfirmed={
          info.topupInfo?.payment_compliance_confirmed === true
        }
        referralRate={wallet.summary?.rules.referral_bonus_rate ?? 0}
        records={rewards.records}
        recordsTotal={rewards.total}
        recordsLoading={rewards.loading}
        recordsError={rewards.error}
        onRecordsRetry={() => void rewards.refetch()}
        recordsPage={page}
        onRecordsPageChange={setPage}
        loading={user.isPending || wallet.loading || affiliate.loading}
      />
      <TransferDialog
        open={transferOpen}
        onOpenChange={setTransferOpen}
        availableQuota={wallet.summary?.referral_available_quota ?? 0}
        transferring={affiliate.transferring || agentTransferring}
        allowAgentTarget={balance.data?.status === 'active'}
        onConfirm={async (quota, target) => {
          let success = false
          if (target === 'agent') {
            setAgentTransferring(true)
            try {
              await postPlatform('/simple/finance/referral-transfer', {
                request_id: identify({ quota }),
                quota,
              })
              identify.reset()
              success = true
            } catch (error) {
              handleServerError(error)
            } finally {
              setAgentTransferring(false)
            }
          } else {
            success = await affiliate.transferQuota(quota)
          }
          if (success) {
            await client.invalidateQueries({ queryKey: ['wallet'] })
            await client.invalidateQueries({ queryKey: ['agent-platform'] })
          }
          return success
        }}
      />
      {withdrawalOpen &&
        (isAgent && (balance.isPending || balance.isError) ? (
          <Dialog
            open
            title={t('Contact support to withdraw')}
            onOpenChange={setWithdrawalOpen}
          >
            {balance.isPending ? (
              <LoadingState />
            ) : (
              <ErrorState
                onRetry={() => {
                  void balance.refetch()
                }}
              />
            )}
          </Dialog>
        ) : (
          <WithdrawalContactDialog
            userId={user.data?.id ?? 0}
            referralQuota={wallet.summary?.referral_available_quota ?? 0}
            paymentQuota={balance.data?.payment_available_quota ?? 0}
            amountDue={balance.data?.amount_due_quota}
            recoveryDebt={balance.data?.payment_debt_quota}
            onClose={() => setWithdrawalOpen(false)}
          />
        ))}
    </>
  )
}
