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

import { Alert, AlertDescription } from '@/components/ui/alert'

import { parsePriceSnapshot } from '../lib/price-snapshot'
import { WorkflowValue, type WorkflowColumn } from './workflow-table'

export function PriceSnapshot(props: { value?: string }) {
  const { t } = useTranslation()
  if (!props.value) return null
  const snapshot = parsePriceSnapshot(props.value)
  if (!snapshot) {
    return (
      <Alert variant='destructive'>
        <AlertDescription>
          {t(
            'The stored pricing snapshot could not be verified. Do not rely on an inferred rate.'
          )}
        </AlertDescription>
      </Alert>
    )
  }
  const columns: WorkflowColumn[] = [
    { key: 'hit_reason', label: 'Pricing decision', kind: 'state' },
    { key: 'agreement_id', label: 'Agreement ID' },
    { key: 'agreement_version', label: 'Terms version' },
    { key: 'tier_key', label: 'Tier key' },
    { key: 'rate_bps', label: 'Price (basis points)', kind: 'integer' },
    { key: 'quota_numerator', label: 'Quota conversion numerator' },
    { key: 'quota_denominator', label: 'Quota conversion denominator' },
    { key: 'face_minor', label: 'Face amount', kind: 'money' },
    { key: 'base_paid_minor', label: 'Amount at base rate', kind: 'money' },
    { key: 'paid_minor', label: 'Quoted payment', kind: 'money' },
    { key: 'policy_version', label: 'Policy version' },
    { key: 'profile_version', label: 'Permission version' },
  ]
  return (
    <section className='space-y-3'>
      <h3 className='font-medium'>{t('Frozen pricing snapshot')}</h3>
      <dl className='grid grid-cols-1 gap-3 text-sm sm:grid-cols-2'>
        {columns.map((column) => (
          <div key={column.key}>
            <dt className='text-muted-foreground'>{t(column.label)}</dt>
            <dd>
              <WorkflowValue row={snapshot} column={column} />
            </dd>
          </div>
        ))}
      </dl>
    </section>
  )
}
