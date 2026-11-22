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
import { StatusBadge } from '@/components/status-badge'

import type { InvoiceStatus } from '../contracts'
import { statusNames, useFinanceLabels } from './i18n'

const variants = {
  awaiting_payment: 'warning',
  pending: 'warning',
  issued: 'success',
  rejected: 'danger',
  cancelled: 'neutral',
} as const

export function ReviewStatus(props: {
  status: InvoiceStatus
  refundReview?: boolean
  feeRefundReview?: boolean
  processing?: boolean
  claimant?: number
  reviewer?: number
}) {
  const { label } = useFinanceLabels()
  const names = statusNames[props.status] ?? ['Unknown status', '未知状态']
  let text = label(...names)
  if (props.status === 'pending' && props.processing) {
    if (props.claimant && props.claimant === props.reviewer) {
      text = label('Claimed by you', '由您开票中')
    } else if (props.claimant) {
      text = `${label('Claimed by reviewer', '开票处理中：审核员')} #${props.claimant}`
    } else {
      text = label('Issuance in progress', '开票处理中')
    }
  }
  return (
    <span className='inline-flex flex-wrap gap-1'>
      <StatusBadge
        variant={variants[props.status] ?? 'neutral'}
        label={text}
        copyable={false}
      />
      {props.feeRefundReview && (
        <StatusBadge
          variant='warning'
          label={label('Fee refund review', '手续费退款待人工核对')}
          copyable={false}
        />
      )}
      {props.refundReview && (
        <StatusBadge
          variant='warning'
          label={label('Refund review', '退款待核对')}
          copyable={false}
        />
      )}
    </span>
  )
}
