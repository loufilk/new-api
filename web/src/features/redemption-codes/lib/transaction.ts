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
import type {
  OperationDraft,
  OperationPayload,
} from '@/features/agent-platform/types'
import { ROLE } from '@/lib/roles'

import type { Redemption } from '../types'

/** The backend derives authority again; this only selects its matching prepare contract. */
export function redemptionOperation(
  operation: 'create' | 'update' | 'delete',
  role: number | undefined,
  payload: OperationPayload,
  endpoint: string,
  review: OperationDraft['review'] = []
): OperationDraft {
  return {
    action: `redemption.${role !== undefined && role >= ROLE.ADMIN ? 'admin.' : ''}${operation}`,
    request_id: crypto.randomUUID(),
    payload,
    endpoint,
    resource: '/redemption-codes',
    method: operation === 'update' ? 'put' : 'post',
    review,
  }
}

/** Bound deliveries and paid-source reservations must not use legacy release paths. */
export function isProtectedRedemption(code: Redemption): boolean {
  return (
    code.source_protected === true ||
    (code.customer_user_id ?? 0) > 0 ||
    (code.delivery_id ?? 0) > 0
  )
}
