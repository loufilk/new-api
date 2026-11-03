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
