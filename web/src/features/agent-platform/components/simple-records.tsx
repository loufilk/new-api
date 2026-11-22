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
