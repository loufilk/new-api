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
import type { TFunction } from 'i18next'

import type { SupportTicketCategory } from '../types'

export const SUPPORT_TICKET_CATEGORIES: SupportTicketCategory[] = [
  'billing',
  'api_model',
  'account_access',
  'other',
]

export function supportTicketCategoryLabel(
  t: TFunction,
  category?: string
): string {
  switch (category) {
    case 'billing':
      return t('Billing and payments')
    case 'api_model':
      return t('API and models')
    case 'account_access':
      return t('Account and permissions')
    default:
      return t('Other issues')
  }
}
