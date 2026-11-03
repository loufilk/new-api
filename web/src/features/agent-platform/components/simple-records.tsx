import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'

import { SimpleCodeRecords } from './simple-code-records'
import { SimplePaymentRecords } from './simple-payment-records'

export function SimpleRecords(props: { agentUserId?: number }) {
  const { t } = useTranslation()
  const [mode, setMode] = useState<'all' | 'direct' | 'payment'>('all')
  return (
    <div className='space-y-4'>
      <NativeSelect
        className='w-44'
        aria-label={t('Filter code issue method')}
        value={mode}
        onChange={(event) => setMode(event.target.value as typeof mode)}
      >
        <NativeSelectOption value='all'>{t('All methods')}</NativeSelectOption>
        <NativeSelectOption value='direct'>
          {t('Direct issue')}
        </NativeSelectOption>
        <NativeSelectOption value='payment'>
          {t('Customer payment')}
        </NativeSelectOption>
      </NativeSelect>
      {mode !== 'payment' && (
        <SimpleCodeRecords
          key={`direct-${props.agentUserId}-${mode}`}
          agentUserId={props.agentUserId}
          mode='direct'
          hideFilter
        />
      )}
      {mode !== 'direct' && (
        <SimplePaymentRecords
          key={`payment-${props.agentUserId}-${mode}`}
          agentUserId={props.agentUserId}
        />
      )}
    </div>
  )
}
