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
import { Activity, BarChart3, CircleDollarSign, Gift } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { IconBadge, type IconBadgeTone } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import { formatQuotaWithCurrency } from '@/lib/currency'
import { cn } from '@/lib/utils'

import type { UserWalletData, WalletSummary } from '../types'

interface WalletStatsCardProps {
  user: UserWalletData | null
  summary: WalletSummary | null
  userLoading?: boolean
  summaryLoading?: boolean
}

function formatWalletQuota(quota: number): string {
  return formatQuotaWithCurrency(quota, {
    digitsLarge: 2,
    digitsSmall: 4,
    minimumFractionDigits: 2,
    abbreviate: true,
  })
}

export function WalletStatsCard(props: WalletStatsCardProps) {
  const { t } = useTranslation()

  const stats: {
    key: 'paid' | 'bonus' | 'usage' | 'requests'
    label: string
    value: string
    description: string
    icon: typeof CircleDollarSign
    tone: IconBadgeTone
    loading: boolean
  }[] = [
    {
      key: 'paid',
      label: t('Paid Balance'),
      value: formatWalletQuota(props.summary?.paid ?? 0),
      description: t('Purchased and redeemed quota'),
      icon: CircleDollarSign,
      tone: 'info',
      loading: props.summaryLoading === true,
    },
    {
      key: 'bonus',
      label: t('Bonus Balance'),
      value: formatWalletQuota(
        (props.summary?.bonus ?? 0) + (props.summary?.reward ?? 0)
      ),
      description: t(
        'Signup bonus applies to eligible models; invitation rewards apply to all models'
      ),
      icon: Gift,
      tone: 'chart-2',
      loading: props.summaryLoading === true,
    },
    {
      key: 'usage',
      label: t('Total Usage'),
      value: formatWalletQuota(props.user?.used_quota ?? 0),
      description: t('Total consumed quota'),
      icon: BarChart3,
      tone: 'info',
      loading: props.userLoading === true,
    },
    {
      key: 'requests',
      label: t('API Requests'),
      value: (props.user?.request_count ?? 0).toLocaleString(),
      description: t('Total requests made'),
      icon: Activity,
      tone: 'chart-4',
      loading: props.userLoading === true,
    },
  ]

  return (
    <div
      data-wallet-stats-grid
      className='grid grid-cols-2 overflow-hidden rounded-lg border lg:grid-cols-4'
    >
      {stats.map((item, index) => (
        <div
          key={item.key}
          data-wallet-stat={item.key}
          className={cn(
            'min-w-0 px-2.5 py-2.5 sm:px-4 sm:py-3',
            index % 2 === 1 && 'border-l',
            index >= 2 && 'border-t lg:border-t-0',
            index > 0 && 'lg:border-l'
          )}
        >
          {item.loading ? (
            <>
              <Skeleton className='h-3.5 w-full' />
              <Skeleton className='mt-2 h-6 w-full sm:h-7' />
              <Skeleton className='mt-1.5 hidden h-3.5 w-24 md:block' />
            </>
          ) : (
            <>
              <div className='flex flex-wrap items-center justify-between gap-2'>
                <div className='flex items-center gap-1.5'>
                  <IconBadge tone={item.tone} size='stat'>
                    <item.icon />
                  </IconBadge>
                  <div className='text-muted-foreground text-xs font-medium'>
                    {item.label}
                  </div>
                </div>
                <div
                  data-wallet-stat-value
                  className='text-foreground min-w-0 font-mono text-sm font-bold tracking-tight break-all tabular-nums sm:text-lg'
                >
                  {item.value}
                </div>
              </div>
              <div className='text-muted-foreground mt-1 hidden text-xs md:block'>
                {item.description}
              </div>
            </>
          )}
        </div>
      ))}
    </div>
  )
}
