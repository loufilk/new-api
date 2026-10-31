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
import type { IssuedDeliveryLink } from '../components/delivery-claim-link'

export function isIssuedDeliveryLink(
  value: unknown
): value is IssuedDeliveryLink {
  return Boolean(
    value &&
    typeof value === 'object' &&
    'id' in value &&
    typeof value.id === 'number' &&
    Number.isSafeInteger(value.id) &&
    value.id > 0 &&
    'quota' in value &&
    typeof value.quota === 'number' &&
    Number.isSafeInteger(value.quota) &&
    value.quota > 0 &&
    'claim_code' in value &&
    typeof value.claim_code === 'string' &&
    value.claim_code.length > 0
  )
}
