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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  SideDrawerSection,
  SideDrawerSectionHeader,
} from '@/components/drawer-layout'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { hasPermission } from '@/lib/admin-permissions'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { callInterceptionKey, getCallInterception } from './api'
import { CallInterceptionForm } from './form'
import { CallInterceptionLogs } from './logs'

export type UserCallInterceptionSectionProps = {
  userId: number
  targetRole: number
}

export function UserCallInterceptionSection(
  props: UserCallInterceptionSectionProps
) {
  const viewer = useAuthStore((state) => state.auth.user)
  const canManageRole =
    viewer &&
    viewer.role >= ROLE.ADMIN &&
    (viewer.role === ROLE.SUPER_ADMIN || viewer.role > props.targetRole)
  if (
    !canManageRole ||
    !hasPermission(viewer, 'user_call_interception', 'read')
  ) {
    return null
  }

  return (
    <AuthorizedInterceptionSection
      key={props.userId}
      userId={props.userId}
      canUpdate={hasPermission(viewer, 'user_call_interception', 'update')}
    />
  )
}

function AuthorizedInterceptionSection(props: {
  userId: number
  canUpdate: boolean
}) {
  const { t } = useTranslation()
  const [tab, setTab] = useState<string>('configuration')
  const query = useQuery({
    queryKey: callInterceptionKey(props.userId),
    queryFn: ({ signal }) => getCallInterception(props.userId, signal),
    refetchOnWindowFocus: false,
    gcTime: 0,
  })

  return (
    <SideDrawerSection>
      <SideDrawerSectionHeader
        title={t('User-level call interception and custom replies')}
        description={t(
          'Applies to every valid API key and Playground. Account status, balance, and keys stay unchanged.'
        )}
      />
      <Tabs value={tab} onValueChange={(value) => setTab(String(value))}>
        <TabsList className='h-auto max-w-full flex-wrap'>
          <TabsTrigger value='configuration'>{t('Configuration')}</TabsTrigger>
          <TabsTrigger value='hits'>{t('Interception hits')}</TabsTrigger>
          <TabsTrigger value='audits'>{t('Configuration audit')}</TabsTrigger>
        </TabsList>
        <TabsContent value='configuration' keepMounted>
          {query.isPending && <LoadingState size='sm' />}
          {query.isError && !query.data && (
            <ErrorState
              title={t('Failed to load interception settings')}
              onRetry={() => {
                void query.refetch()
              }}
              className='min-h-32'
            />
          )}
          {query.data && (
            <CallInterceptionForm
              key={props.userId}
              config={query.data}
              canUpdate={props.canUpdate}
              refreshing={query.isFetching}
              onReload={async () => {
                const result = await query.refetch()
                if (result.error) throw result.error
                if (!result.data) {
                  throw new Error(t('Failed to load interception settings'))
                }
                return result.data
              }}
            />
          )}
        </TabsContent>
        <TabsContent value='hits'>
          {tab === 'hits' && (
            <CallInterceptionLogs userId={props.userId} kind='hits' />
          )}
        </TabsContent>
        <TabsContent value='audits'>
          {tab === 'audits' && (
            <CallInterceptionLogs userId={props.userId} kind='audits' />
          )}
        </TabsContent>
      </Tabs>
    </SideDrawerSection>
  )
}
