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
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import {
  SideDrawerSection,
  SideDrawerSectionHeader,
} from '@/components/drawer-layout'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { hasPermission } from '@/lib/admin-permissions'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { getUserGroupPricing, userGroupPricingKey } from './api'
import { UserGroupPricingForm } from './form'

export function UserGroupPricingSection(props: {
  userId: number
  targetRole: number
}) {
  const viewer = useAuthStore((state) => state.auth.user)
  const canManageRole =
    viewer &&
    viewer.role >= ROLE.ADMIN &&
    (viewer.role === ROLE.SUPER_ADMIN || viewer.role > props.targetRole)
  if (!canManageRole || !hasPermission(viewer, 'user_group_pricing', 'read')) {
    return null
  }
  return (
    <AuthorizedGroupPricingSection
      key={props.userId}
      userId={props.userId}
      canUpdate={hasPermission(viewer, 'user_group_pricing', 'update')}
    />
  )
}

function AuthorizedGroupPricingSection(props: {
  userId: number
  canUpdate: boolean
}) {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey: userGroupPricingKey(props.userId),
    queryFn: ({ signal }) => getUserGroupPricing(props.userId, signal),
    refetchOnWindowFocus: false,
    retry: false,
    gcTime: 0,
    meta: { errorToast: false },
  })
  return (
    <SideDrawerSection>
      <SideDrawerSectionHeader
        title={t('Personal group pricing')}
        description={t(
          'The personal multiplier replaces the default multiplier for this user and billing group.'
        )}
      />
      {query.isPending && <LoadingState size='sm' />}
      {query.isError && !query.data && (
        <ErrorState
          title={t('Failed to load personal group pricing')}
          onRetry={() => void query.refetch()}
          className='min-h-32'
        />
      )}
      {query.data && (
        <UserGroupPricingForm
          config={query.data}
          canUpdate={props.canUpdate}
          refreshing={query.isFetching}
          onReload={async () => {
            const result = await query.refetch()
            if (result.error) throw result.error
            if (!result.data) {
              throw new Error(t('Failed to load personal group pricing'))
            }
            return result.data
          }}
        />
      )}
    </SideDrawerSection>
  )
}
