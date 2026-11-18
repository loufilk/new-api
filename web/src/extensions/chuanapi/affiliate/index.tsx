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

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Card, CardContent } from '@/components/ui/card'
import { getSelf } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import { getTopupInfo } from '../wallet/api'
import { AffiliateRewardsCard } from '../wallet/components/affiliate-rewards-card'
import { TransferDialog } from '../wallet/components/dialogs/transfer-dialog'
import { REFERRAL_RECORDS_PAGE_SIZE } from '../wallet/constants'
import {
  useAffiliate,
  useReferralRewards,
  useWalletSummary,
} from '../wallet/hooks'
import type { UserWalletData } from '../wallet/types'
import { ReferralWithdrawals } from './withdrawals'

export function AffiliatePage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [page, setPage] = useState(1)
  const [transferOpen, setTransferOpen] = useState(false)
  const affiliate = useAffiliate()
  const wallet = useWalletSummary()
  const rewards = useReferralRewards(page, REFERRAL_RECORDS_PAGE_SIZE)
  const user = useQuery({
    queryKey: ['affiliate', 'self'],
    queryFn: async () =>
      requireServerSuccess(await getSelf()).data as UserWalletData,
  })
  const compliance = useQuery({
    queryKey: ['affiliate', 'compliance'],
    queryFn: async () => requireServerSuccess(await getTopupInfo()).data,
  })
  const loadFailed =
    user.isError || !!wallet.error || compliance.isError || !!affiliate.error
  const ready = !!user.data && !!wallet.summary && !!compliance.data

  const transfer = async (quota: number): Promise<boolean> => {
    if (
      loadFailed ||
      !ready ||
      compliance.data?.payment_compliance_confirmed === false
    ) {
      return false
    }
    if (!(await affiliate.transferQuota(quota))) return false
    await Promise.all([
      user.refetch(),
      wallet.refetch(),
      rewards.refetch(),
      queryClient.invalidateQueries({ queryKey: ['referral-withdrawals'] }),
    ])
    return true
  }

  return (
    <>
      <SectionPageLayout>
        <SectionPageLayout.Title>
          {t('Invitation Rewards')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Description>
          {t(
            'Share your invitation link, track qualifying rewards and choose how to use them.'
          )}
        </SectionPageLayout.Description>
        <SectionPageLayout.Content>
          <div className='mx-auto flex w-full max-w-7xl flex-col gap-5'>
            <Card>
              <CardContent className='grid gap-5 sm:grid-cols-3'>
                {[
                  [
                    'Share your link',
                    'Friends register through your personal invitation link.',
                  ],
                  [
                    'Earn from eligible payments',
                    'Qualifying top-ups create rewards with a visible settlement status.',
                  ],
                  [
                    'Use available rewards',
                    'Transfer released rewards to your wallet or follow the withdrawal process.',
                  ],
                ].map(([title, description], index) => (
                  <div key={title} className='flex items-start gap-3'>
                    <span className='bg-primary/10 text-primary flex size-9 shrink-0 items-center justify-center rounded-xl text-sm font-semibold'>
                      0{index + 1}
                    </span>
                    <div>
                      <h2 className='text-sm font-semibold'>{t(title)}</h2>
                      <p className='text-muted-foreground mt-2 text-xs leading-relaxed'>
                        {t(description)}
                      </p>
                    </div>
                  </div>
                ))}
              </CardContent>
            </Card>
            <Alert>
              <AlertDescription>{t('affiliate.scope')}</AlertDescription>
            </Alert>
            {loadFailed ? (
              <ErrorState
                onRetry={() => {
                  void user.refetch()
                  void wallet.refetch()
                  void compliance.refetch()
                  void affiliate.refetch()
                }}
              />
            ) : null}
            {!ready && !loadFailed && <LoadingState />}
            {ready && !loadFailed && (
              <AffiliateRewardsCard
                standalone
                availableQuota={wallet.summary?.referral_available_quota ?? 0}
                user={user.data ?? null}
                affiliateCode={affiliate.affiliateCode}
                affiliateLink={affiliate.affiliateLink}
                onTransfer={() => setTransferOpen(true)}
                complianceConfirmed={
                  compliance.data?.payment_compliance_confirmed !== false
                }
                referralRate={wallet.summary?.rules.referral_bonus_rate ?? 0}
                records={rewards.records}
                recordsTotal={rewards.total}
                recordsLoading={rewards.loading}
                recordsError={rewards.error}
                onRecordsRetry={() => void rewards.refetch()}
                recordsPage={page}
                onRecordsPageChange={setPage}
                loading={affiliate.loading}
              />
            )}
            <ReferralWithdrawals />
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>
      <TransferDialog
        open={transferOpen}
        onOpenChange={setTransferOpen}
        onConfirm={transfer}
        availableQuota={wallet.summary?.referral_available_quota ?? 0}
        transferring={affiliate.transferring}
      />
    </>
  )
}
