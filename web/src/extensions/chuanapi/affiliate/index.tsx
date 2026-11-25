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
import {
  ArrowDownToLine,
  ArrowUpRight,
  CircleHelp,
  Gift,
  Link2,
  Percent,
  ReceiptText,
  RefreshCw,
  Users,
  WalletCards,
} from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Button, buttonVariants } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { TitledCard } from '@/components/ui/titled-card'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatQuota } from '@/lib/format'
import { requireServerSuccess } from '@/lib/server-error-message'

import { getTopupInfo } from '../wallet/api'
import { TransferDialog } from '../wallet/components/dialogs/transfer-dialog'
import { ReferralCommissionRecords } from '../wallet/components/referral-commission-records'
import { REFERRAL_RECORDS_PAGE_SIZE } from '../wallet/constants'
import {
  useAffiliate,
  useReferralRewards,
  useWalletSummary,
} from '../wallet/hooks'
import { formatReferralRate } from '../wallet/lib'

export function AffiliatePage() {
  const { t, i18n } = useTranslation()
  const [page, setPage] = useState(1)
  const [transferOpen, setTransferOpen] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const affiliate = useAffiliate()
  const wallet = useWalletSummary()
  const rewards = useReferralRewards(page, REFERRAL_RECORDS_PAGE_SIZE)
  const compliance = useQuery({
    queryKey: ['affiliate', 'compliance'],
    queryFn: async () => requireServerSuccess(await getTopupInfo()).data,
  })

  const summary = wallet.summary
  const ready = !!summary && !!compliance.data && !affiliate.loading
  const failed = !!wallet.error || compliance.isError || !!affiliate.error
  const compliant =
    compliance.data?.payment_compliance_confirmed === true &&
    summary?.rules.referral_bonus_enabled === true
  const availableQuota = summary?.referral_available_quota ?? 0
  const rate = formatReferralRate(summary?.rules.referral_bonus_rate ?? 0)
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)

  const refresh = async () => {
    setRefreshing(true)
    try {
      await Promise.all([
        wallet.refetch(),
        compliance.refetch(),
        affiliate.refetch(),
        rewards.refetch(),
      ])
    } finally {
      setRefreshing(false)
    }
  }

  const transfer = async (quota: number, target: 'wallet' | 'agent') => {
    if (
      !ready ||
      failed ||
      !compliant ||
      target !== 'wallet' ||
      quota > availableQuota
    ) {
      return false
    }
    if (!(await affiliate.transferQuota(quota))) return false
    await Promise.all([wallet.refetch(), rewards.refetch()])
    return true
  }

  return (
    <>
      <SectionPageLayout>
        <SectionPageLayout.Title>
          {t('Invitation Rewards')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <Button
            variant='outline'
            size='sm'
            onClick={() => void refresh()}
            disabled={refreshing}
            aria-busy={refreshing}
          >
            <RefreshCw aria-hidden='true' />
            {t('Refresh')}
          </Button>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <div className='mx-auto flex w-full max-w-7xl flex-col gap-4 sm:gap-5'>
            <div className='space-y-1 text-sm'>
              <p>{t('Invite friends to earn extra rewards')}</p>
              <p className='text-muted-foreground'>{t('affiliate.scope')}</p>
            </div>

            {failed && <ErrorState onRetry={() => void refresh()} />}
            {!ready && !failed && <LoadingState />}
            {ready && !failed && summary && (
              <>
                <div className='grid grid-cols-2 gap-3 sm:gap-4 lg:grid-cols-4'>
                  {[
                    { icon: Percent, label: t('affiliate.rate'), value: rate },
                    {
                      icon: Users,
                      label: t('Referral Invited Users'),
                      value: formatNumber(
                        summary.referral_invite_count,
                        locale
                      ),
                    },
                    {
                      icon: Gift,
                      label: t('Referral Rewards Available'),
                      value: formatQuota(availableQuota),
                    },
                    {
                      icon: ReceiptText,
                      label: t('Cumulative referral rewards'),
                      value: formatQuota(summary.referral_total_quota),
                    },
                  ].map(({ icon: Icon, label, value }) => (
                    <Card
                      key={label}
                      data-card-hover='false'
                      className='min-w-0 py-0'
                    >
                      <CardContent className='p-3 sm:p-4'>
                        <dl>
                          <dt className='text-muted-foreground flex items-start gap-2 text-xs font-medium sm:text-sm'>
                            <Icon
                              className='text-primary mt-0.5 size-4 shrink-0'
                              aria-hidden='true'
                            />
                            <span className='min-w-0 wrap-break-word'>
                              {label}
                            </span>
                          </dt>
                          <dd className='mt-2 text-xl font-semibold wrap-break-word tabular-nums sm:text-2xl'>
                            {value}
                          </dd>
                        </dl>
                      </CardContent>
                    </Card>
                  ))}
                </div>

                <div className='grid items-start gap-4 sm:gap-5 lg:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]'>
                  <div className='grid min-w-0 gap-4 sm:gap-5'>
                    <TitledCard
                      title={<h3>{t('Invitation Link')}</h3>}
                      icon={<Link2 aria-hidden='true' />}
                      disableHoverEffect
                      contentClassName='space-y-4'
                    >
                      {affiliate.affiliateCode && affiliate.affiliateLink ? (
                        <>
                          <InvitationField
                            id='affiliate-code'
                            label={t('affiliate.code')}
                            copyLabel={t('affiliate.copyCode')}
                            value={affiliate.affiliateCode}
                          />
                          <InvitationField
                            id='affiliate-link'
                            label={t('Invitation Link')}
                            copyLabel={t('Copy referral link')}
                            value={affiliate.affiliateLink}
                          />
                        </>
                      ) : (
                        <p className='text-muted-foreground text-sm'>
                          {t('Not available')}
                        </p>
                      )}
                    </TitledCard>

                    <TitledCard
                      title={<h3>{t('Reward Rules')}</h3>}
                      icon={<CircleHelp aria-hidden='true' />}
                      disableHoverEffect
                    >
                      <ol className='text-muted-foreground list-decimal space-y-2 ps-5 text-sm'>
                        <li>
                          {t(
                            'Only website online top-ups qualify for invitation rewards.'
                          )}
                        </li>
                        <li>
                          {t(
                            'Earn {{rate}} from every qualifying top-up, with no cap.',
                            { rate }
                          )}
                        </li>
                        <li>
                          {t(
                            'Rewards unlock after five days. Refunds cancel or reverse the matching reward amount.'
                          )}
                        </li>
                      </ol>
                    </TitledCard>
                  </div>

                  <div className='grid min-w-0 gap-4 sm:gap-5'>
                    <TitledCard
                      title={<h3>{t('Transfer Rewards')}</h3>}
                      icon={<WalletCards aria-hidden='true' />}
                      disableHoverEffect
                      contentClassName='space-y-3'
                    >
                      <dl className='bg-muted/35 rounded-lg border p-3 text-sm'>
                        <dt className='text-muted-foreground text-xs'>
                          {t('Transfer target')}
                        </dt>
                        <dd className='mt-1 font-medium'>{t('User wallet')}</dd>
                      </dl>
                      <p className='text-muted-foreground text-sm'>
                        {t(
                          'Transfer released referral rewards 1:1 without a fee.'
                        )}
                      </p>
                      <Button
                        className='h-auto min-h-9 w-full py-2 whitespace-normal'
                        onClick={() => setTransferOpen(true)}
                        disabled={
                          !compliant ||
                          availableQuota <= 0 ||
                          affiliate.transferring
                        }
                      >
                        {t('Referral Transfer to Balance')}
                      </Button>
                      {!compliant && (
                        <p className='text-muted-foreground text-xs'>
                          {t(
                            'Referral reward transfer is disabled until the administrator confirms compliance terms.'
                          )}
                        </p>
                      )}
                    </TitledCard>

                    <TitledCard
                      title={<h3>{t('Contact support to withdraw')}</h3>}
                      icon={<ArrowDownToLine aria-hidden='true' />}
                      disableHoverEffect
                      contentClassName='space-y-3'
                    >
                      <p className='text-muted-foreground text-sm'>
                        {t(
                          'Handled by support. Minimum $20 combined, once per calendar month, with a 2% fee.'
                        )}
                      </p>
                      <a
                        href='/wallet'
                        className={buttonVariants({
                          variant: 'outline',
                          size: 'sm',
                        })}
                      >
                        {t('Wallet')}
                        <ArrowUpRight aria-hidden='true' />
                      </a>
                    </TitledCard>
                  </div>
                </div>

                <TitledCard
                  title={<h3>{t('Commission Records')}</h3>}
                  icon={<ReceiptText aria-hidden='true' />}
                  disableHoverEffect
                  contentClassName='[&_[role=region]]:max-h-96 2xl:[&_[role=region]]:max-h-96'
                >
                  <ReferralCommissionRecords
                    records={rewards.records}
                    total={rewards.total}
                    loading={rewards.loading}
                    error={rewards.error}
                    onRetry={() => void rewards.refetch()}
                    page={page}
                    onPageChange={setPage}
                  />
                </TitledCard>
              </>
            )}
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>
      <TransferDialog
        open={transferOpen}
        onOpenChange={setTransferOpen}
        onConfirm={transfer}
        availableQuota={availableQuota}
        transferring={affiliate.transferring}
      />
    </>
  )
}

function InvitationField({
  id,
  label,
  copyLabel,
  value,
}: {
  id: string
  label: string
  copyLabel: string
  value: string
}) {
  const { t } = useTranslation()
  return (
    <div className='space-y-2'>
      <Label htmlFor={id}>{label}</Label>
      <div className='flex min-w-0 items-center gap-2'>
        <Input
          id={id}
          value={value}
          readOnly
          className='bg-muted/35 h-9 min-w-0 flex-1 font-mono text-xs'
        />
        <CopyButton
          value={value}
          variant='outline'
          size='sm'
          className='h-9'
          tooltip={copyLabel}
          aria-label={copyLabel}
        >
          {t('Copy')}
        </CopyButton>
      </div>
    </div>
  )
}
