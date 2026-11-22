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
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

export function SimpleRecordPager(props: {
  page: number
  total: number
  pageSize?: number
  onChange: (page: number) => void
}) {
  const { t } = useTranslation()
  const pages = Math.max(1, Math.ceil(props.total / (props.pageSize ?? 20)))
  return (
    <div className='flex flex-wrap items-center justify-between gap-2 text-sm'>
      <span className='text-muted-foreground'>
        {t('Page {{page}} of {{pages}}', { page: props.page, pages })}
      </span>
      <div className='flex gap-2'>
        <Button
          size='sm'
          variant='outline'
          disabled={props.page <= 1}
          onClick={() => props.onChange(Math.max(1, props.page - 1))}
        >
          {t('Previous')}
        </Button>
        <Button
          size='sm'
          variant='outline'
          disabled={props.page >= pages}
          onClick={() => props.onChange(props.page + 1)}
        >
          {t('Next')}
        </Button>
      </div>
    </div>
  )
}
