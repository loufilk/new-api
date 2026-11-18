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

type PlaygroundBalanceBarProps = {
  quota: number
}

export function PlaygroundBalanceBar({ quota }: PlaygroundBalanceBarProps) {
  const { t } = useTranslation()

  return (
    <div className='bg-card flex flex-wrap items-center justify-between gap-3 rounded-2xl border px-4 py-3 shadow-xs'>
      <div className='min-w-0'>
        <h1 className='text-foreground text-base font-semibold tracking-tight'>
          {t('Playground')}
        </h1>
        <p className='text-muted-foreground mt-1 hidden text-xs sm:block'>
          {t(
            'Try a model, compare its responses, and inspect the request in your logs.'
          )}
        </p>
      </div>

      <div className='flex items-center gap-2'>
        <span className='text-muted-foreground bg-background/70 flex items-center gap-1.5 rounded-full border px-3 py-1 text-xs font-medium'>
          <WalletIcon className='size-3.5' aria-hidden='true' />
          <span className='hidden sm:inline'>{t('Balance')}</span>
          <span className='text-foreground font-semibold tabular-nums'>
            {formatQuota(quota)}
          </span>
        </span>
        <Button render={<Link to='/wallet' />} size='sm' variant='outline'>
          {t('Recharge')}
        </Button>
      </div>
    </div>
  )
}
