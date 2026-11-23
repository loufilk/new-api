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
import {
  createFileRoute,
  lazyRouteComponent,
  redirect,
} from '@tanstack/react-router'

import { extensionConsoleTools } from '@/extensions'
import { partnerManagementAccess } from '@/extensions/chuanapi/partners/permissions'
import { useAuthStore } from '@/stores/auth-store'

export const Route = createFileRoute(
  '/_authenticated/partner-application-management/'
)({
  beforeLoad: () => {
    if (!partnerManagementAccess(useAuthStore.getState().auth.user).open) {
      throw redirect({ to: '/403', replace: true })
    }
  },
  component: lazyRouteComponent(() =>
    extensionConsoleTools().loadPartnerApplicationManagementPage()
  ),
})
