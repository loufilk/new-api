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
    <div className='flex items-center justify-between gap-2'>
      <h1 className='text-foreground text-sm font-semibold tracking-tight md:text-base'>
        {t('Playground')}
      </h1>

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
