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
import { Link } from '@tanstack/react-router'
import { WalletIcon } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { formatQuota } from '@/lib/format'

type PlaygroundQuotaBlockProps = {
  quota: number
}

export function PlaygroundQuotaBlock({ quota }: PlaygroundQuotaBlockProps) {
  const { t } = useTranslation()

  return (
    <div className='flex min-h-[min(520px,calc(100svh-18rem))] items-center justify-center px-4'>
      <div className='grid w-full max-w-md justify-items-center gap-4 text-center'>
        <div className='bg-destructive/10 text-destructive rounded-2xl p-3.5'>
          <WalletIcon className='size-6' aria-hidden='true' />
        </div>

        <div className='grid gap-1.5'>
          <h2 className='text-xl font-semibold tracking-tight text-balance md:text-2xl'>
            {t('Insufficient balance')}
          </h2>
          <p className='text-muted-foreground text-sm leading-6 text-balance'>
            {t('Add quota to your wallet to start using the Playground.')}
          </p>
        </div>

        <div className='bg-muted text-muted-foreground rounded-full px-4 py-1.5 text-xs font-medium'>
          {t('Balance')}:{' '}
          <span className='text-foreground font-semibold tabular-nums'>
            {formatQuota(quota)}
          </span>
        </div>

        <Button className='mt-1' render={<Link to='/wallet' />}>
          {t('Go to Wallet')}
        </Button>
      </div>
    </div>
  )
}
