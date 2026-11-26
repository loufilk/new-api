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

import { useApiInfo } from '@/features/dashboard/hooks/use-status-data'
import { useStatus } from '@/hooks/use-status'

export function useApiKeyAddresses() {
  const { t } = useTranslation()
  const { status, loading } = useStatus()
  const { items } = useApiInfo()
  const serverAddress =
    typeof status?.server_address === 'string'
      ? status.server_address.trim()
      : ''

  const addresses = items.length
    ? items
    : [
        {
          url: serverAddress || window.location.origin,
          route: serverAddress ? t('Default API address') : t('Current domain'),
          description: '',
        },
      ]
  // Admins can configure the same URL more than once, with different labels
  // or notes. Preserve order and keep each row's clipboard feedback isolated.
  const occurrences = new Map<string, number>()

  return {
    loading,
    addresses: addresses.map((entry) => {
      const identity = JSON.stringify([
        entry.url,
        entry.route,
        entry.description,
      ])
      const occurrence = (occurrences.get(identity) ?? 0) + 1
      occurrences.set(identity, occurrence)
      return { ...entry, key: `${identity}:${occurrence}` }
    }),
  }
}
