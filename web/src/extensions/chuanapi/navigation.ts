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
import { t } from 'i18next'

import type { NavigationExtension } from '../navigation'

export const CHUANAPI_PUBLIC_HOME = 'https://chuanapi.com/'

/** Web-home quickstart URL that used to be the Docs destination. */
export const CHUANAPI_LEGACY_QUICKSTART_DOCS =
  'https://chuanapi.com/docs/quickstart/'

const LEGACY_QUICKSTART_URLS = new Set([
  CHUANAPI_LEGACY_QUICKSTART_DOCS,
  'https://chuanapi.com/docs/quickstart',
  'https://chuanapi.com/#quickstart',
  'https://chuanapi.com#quickstart',
])

export const chuanapiMonitorNavigationExtension: NavigationExtension = {
  id: 'chuanapi/monitor-navigation',
  apply(links) {
    if (links.some((link) => link.href === '/model-monitor')) return [...links]
    const result = [...links]
    const docsIndex = result.findIndex((link) => link.href === '/help')
    result.splice(docsIndex < 0 ? result.length : docsIndex, 0, {
      title: t('monitor.title'),
      href: '/model-monitor',
    })
    return result
  },
}

export const chuanapiNavigationExtension: NavigationExtension = {
  id: 'chuanapi/navigation',
  publicHomeUrl: CHUANAPI_PUBLIC_HOME,
  apply(links) {
    return links.map((link) => {
      if (link.href === '/') {
        return {
          ...link,
          href: CHUANAPI_PUBLIC_HOME,
          documentNavigation: true,
        }
      }

      // The New API documentation lives inside the SPA: keep /help internal
      // and treat every legacy marketing quickstart entry as the same page.
      if (link.href === '/help' || LEGACY_QUICKSTART_URLS.has(link.href)) {
        return {
          ...link,
          href: '/help',
          documentNavigation: false,
          external: false,
        }
      }

      return { ...link }
    })
  },
}
