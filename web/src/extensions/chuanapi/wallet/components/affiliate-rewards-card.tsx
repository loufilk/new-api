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
import { Share2, ChevronDown } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import {
  Collapsible,
  CollapsibleTrigger,
  CollapsibleContent,
} from '@/components/ui/collapsible'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { TitledCard } from '@/components/ui/titled-card'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatQuota } from '@/lib/format'
import { cn } from '@/lib/utils'

import { formatReferralRate } from '../lib'
import type { ReferralCommissionItem, UserWalletData } from '../types'
import { ReferralCommissionRecords } from './referral-commission-records'

interface AffiliateRewardsCardProps {
  user: UserWalletData | null
  affiliateLink: string
  onTransfer: () => void
  onWithdraw?: () => void
  availableQuota: number
  complianceConfirmed?: boolean
  referralRate: number
  records: ReferralCommissionItem[]
  recordsTotal: number
  recordsLoading?: boolean
  recordsError?: unknown
  onRecordsRetry?: () => void
  recordsPage: number
  onRecordsPageChange: (page: number) => void
  loading?: boolean
  standalone?: boolean
  affiliateCode?: string
}

export function AffiliateRewardsCard(props: AffiliateRewardsCardProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)

  if (props.loading) {
    return (
      <Card data-card-hover='false' className='h-full py-0'>
        <CardContent className='grid gap-4 p-3 sm:p-4'>
          <div>
            <Skeleton className='h-5 w-32' />
            <Skeleton className='mt-2 h-4 w-48' />
          </div>
          <Skeleton className='h-14 rounded-lg' />
          <Skeleton className='h-10 rounded-lg' />
          <Skeleton className='h-24 rounded-lg' />
        </CardContent>
      </Card>
    )
  }

  const hasRewards = props.availableQuota > 0
  const stats = [
    [t('Referral Rewards Available'), formatQuota(props.availableQuota)],
    [
      t('Cumulative referral rewards'),
      formatQuota(props.user?.aff_history_quota ?? 0),
    ],
    [
      t('Referral Invited Users'),
      formatNumber(props.user?.aff_count ?? 0, locale),
    ],
  ]

  if (props.standalone) {
    stats.unshift([t('affiliate.rate'), formatReferralRate(props.referralRate)])
  }

  return (
    <TitledCard
      title={t('Invitation Rewards')}
      description={t('Invite friends to earn extra rewards')}
      icon={<Share2 className='size-4' />}
      iconTone='chart-3'
      disableHoverEffect
      className='h-full'
      headerClassName='sm:p-3 sm:!pb-3'
      titleClassName='text-base sm:text-lg'
      descriptionClassName='sm:text-xs'
      contentClassName='space-y-3 sm:p-3'
      action={
        <div className='flex flex-wrap gap-2'>
          {hasRewards || props.standalone ? (
            <Button
              onClick={props.onTransfer}
              disabled={!props.complianceConfirmed || !hasRewards}
              size='sm'
              variant='outline'
            >
              {t('Referral Transfer to Balance')}
            </Button>
          ) : null}
          {props.onWithdraw && (
            <Button size='sm' variant='outline' onClick={props.onWithdraw}>
              {t('Contact support to withdraw')}
            </Button>
          )}
        </div>
      }
    >
      <div
        className={cn(
          'bg-muted/40 grid gap-3 rounded-lg border p-3 text-center',
          props.standalone ? 'grid-cols-2 sm:grid-cols-4' : 'grid-cols-3'
        )}
      >
        {stats.map(([label, value]) => (
          <div key={label}>
            <div className='text-muted-foreground text-xs font-medium'>
              {label}
            </div>
            <div className='mt-1 text-lg font-semibold break-all tabular-nums'>
              {value}
            </div>
          </div>
        ))}
      </div>

      {props.standalone && props.affiliateCode && (
        <div className='flex items-center gap-2'>
          <span className='text-muted-foreground text-sm'>
            {t('affiliate.code')}
          </span>
          <code className='min-w-0 break-all'>{props.affiliateCode}</code>
          <CopyButton
            value={props.affiliateCode}
            aria-label={t('affiliate.copyCode')}
          />
        </div>
      )}
      <div className='flex items-center gap-2'>
        <Input
          value={props.affiliateLink}
          readOnly
          aria-label={t('Invitation Link')}
          className='border-muted bg-background/70 h-9 min-w-0 flex-1 font-mono text-xs'
        />
        <CopyButton
          value={props.affiliateLink}
          variant='outline'
          size='sm'
          className='bg-background h-9 shrink-0 px-2.5'
          iconClassName='size-4'
          tooltip={t('Copy referral link')}
          aria-label={t('Copy referral link')}
        >
          {t('Copy')}
        </CopyButton>
      </div>
      {!props.complianceConfirmed ? (
        <p className='text-muted-foreground text-xs'>
          {t(
            'Referral reward transfer is disabled until the administrator confirms compliance terms.'
          )}
        </p>
      ) : null}

      <Collapsible className='space-y-2 rounded-lg border p-2'>
        <CollapsibleTrigger className='flex w-full items-center justify-between gap-2 text-sm font-semibold'>
          {t('Reward Rules')}
          <ChevronDown className='size-4' aria-hidden />
        </CollapsibleTrigger>
        <CollapsibleContent>
          <ol className='text-muted-foreground list-decimal space-y-1 ps-4 text-xs'>
            <li>
              {t('Only website online top-ups qualify for invitation rewards.')}
            </li>
            <li>
              {t('Earn {{rate}} from every qualifying top-up, with no cap.', {
                rate: formatReferralRate(props.referralRate),
              })}
            </li>
            <li>
              {t(
                'Rewards unlock after five days. Refunds cancel or reverse the matching reward amount.'
              )}
            </li>
            <li>
              {t(
                'Handled by support. Minimum $20 combined, once per calendar month, with a 2% fee.'
              )}
            </li>
          </ol>
        </CollapsibleContent>
      </Collapsible>

      <Collapsible className='space-y-2 border-t pt-2'>
        <CollapsibleTrigger className='flex w-full items-center justify-between gap-2 text-sm font-semibold'>
          {t('Commission Records')}
          <ChevronDown className='size-4' aria-hidden />
        </CollapsibleTrigger>
        <CollapsibleContent>
          <ReferralCommissionRecords
            records={props.records}
            total={props.recordsTotal}
            loading={props.recordsLoading}
            error={props.recordsError}
            onRetry={props.onRecordsRetry}
            page={props.recordsPage}
            onPageChange={props.onRecordsPageChange}
          />
        </CollapsibleContent>
      </Collapsible>
    </TitledCard>
  )
}
