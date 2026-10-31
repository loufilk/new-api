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
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { useAuthStore } from '@/stores/auth-store'

import { getPlatform } from '../api'
import { ActionForm } from './action-form'

/** Explicit account targeting and page boundaries for account-scoped financial histories. */
export function AccountWorkspace<T>(props: {
  resource: string
  finance?: boolean
  paginate?: boolean
  pathForTarget?: (id: number) => string
  children: (data: T, agentUserId: number) => ReactNode
}) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const [target, setTarget] = useState<number | null>(null)
  const [selecting, setSelecting] = useState(false)
  const [offset, setOffset] = useState(0)
  const agentUserId = props.finance ? target : user?.id
  const query = useQuery({
    queryKey: [
      'agent-platform',
      user?.id,
      user?.role,
      user?.agent_profile?.version,
      props.resource,
      agentUserId,
      offset,
    ],
    queryFn: ({ signal }) =>
      getPlatform<T>(
        props.pathForTarget && agentUserId
          ? props.pathForTarget(agentUserId)
          : props.resource,
        { agent_user_id: agentUserId ?? undefined, offset, limit: 100 },
        signal
      ),
    enabled: !!agentUserId,
  })
  const canContinue =
    query.data &&
    typeof query.data === 'object' &&
    'has_more' in query.data &&
    query.data.has_more === true
  return (
    <div className='flex flex-col gap-4'>
      {props.finance && (
        <div className='flex flex-wrap items-center gap-3'>
          <Button variant='outline' onClick={() => setSelecting(true)}>
            {t('Find user')}
          </Button>
          {agentUserId && (
            <span>
              {t('User ID')}: {agentUserId}
            </span>
          )}
        </div>
      )}
      {selecting && (
        <ActionForm
          title={t('Find user')}
          fields={[
            {
              name: 'agent_user_id',
              label: 'User ID',
              kind: 'integer',
              min: 1,
            },
          ]}
          onClose={() => setSelecting(false)}
          onSubmit={(values) => {
            setTarget(Number(values.agent_user_id))
            setOffset(0)
            setSelecting(false)
          }}
        />
      )}
      {!agentUserId && (
        <p className='text-muted-foreground'>
          {t('Select an account to review its financial records.')}
        </p>
      )}
      {!!agentUserId && query.isPending && <LoadingState />}
      {query.isError && (
        <ErrorState
          onRetry={() => {
            void query.refetch()
          }}
        />
      )}
      {!!agentUserId && query.data && props.children(query.data, agentUserId)}
      {props.paginate !== false && !!agentUserId && query.data && (
        <div className='flex flex-wrap items-center gap-3'>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Historical records are paged. Account balances are current; this page is not a whole-system reconciliation.'
            )}
          </p>
          <Button
            variant='outline'
            disabled={offset === 0 || query.isFetching}
            onClick={() => setOffset(Math.max(0, offset - 100))}
          >
            {t('Previous')}
          </Button>
          <Button
            variant='outline'
            disabled={!canContinue || query.isFetching}
            onClick={() => setOffset(offset + 100)}
          >
            {t('Next')}
          </Button>
        </div>
      )}
    </div>
  )
}
