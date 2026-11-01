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
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

type Node = {
  channel_id: number
  state: 'ready' | 'unconfigured' | 'unavailable'
  count: number
  error_code?: string
}
type Diagnostics = {
  configured: boolean
  invalid_config: boolean
  nodes: Node[]
  cache_scope: 'per_process'
  cache_ttl_seconds: number
  stale_after_seconds: number
  automatic_collection: false
}

export function PoolDiagnostics() {
  const { t } = useTranslation()
  const isRoot = useAuthStore(
    (state) => state.auth.user?.role === ROLE.SUPER_ADMIN
  )
  const [open, setOpen] = useState(false)
  const query = useQuery({
    queryKey: ['account-pool', 'root-diagnostics'],
    enabled: isRoot && open,
    retry: false,
    meta: { errorToast: false },
    queryFn: async ({ signal }) =>
      requireServerSuccess(
        (
          await api.get<{ success: boolean; data: Diagnostics }>(
            '/api/option/account-pool',
            { signal }
          )
        ).data
      ).data,
  })
  if (!isRoot) return null
  const columns: StaticDataTableColumn<Node>[] = [
    { id: 'channel', header: t('Channel ID'), cell: (node) => node.channel_id },
    {
      id: 'state',
      header: t('Status'),
      cell: (node) => t(`pool.node.${node.state}`),
    },
    { id: 'count', header: t('Count'), cell: (node) => node.count },
    {
      id: 'error',
      header: t('pool.diagnosticReason'),
      cell: (node) =>
        node.error_code ? t(`pool.diagnostic.${node.error_code}`) : '—',
    },
  ]
  return (
    <Collapsible
      open={open}
      onOpenChange={setOpen}
      className='rounded-lg border p-3'
    >
      <CollapsibleTrigger
        render={
          <Button
            variant='outline'
            className='h-auto min-h-8 max-w-full py-2 text-start whitespace-normal'
          />
        }
      >
        {t('pool.diagnostics')}
      </CollapsibleTrigger>
      <CollapsibleContent className='pt-4'>
        <div className='flex flex-col gap-3'>
          <Alert>
            <AlertDescription>{t('pool.diagnosticsScope')}</AlertDescription>
          </Alert>
          {query.isPending && <LoadingState />}
          {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
          {query.data && (
            <>
              {query.data.invalid_config && (
                <p role='alert'>{t('pool.invalidConfig')}</p>
              )}
              {!query.data.configured && <p>{t('pool.notConfigured')}</p>}
              <p className='text-muted-foreground text-sm'>
                {t('pool.cachePolicy', {
                  ttl: query.data.cache_ttl_seconds,
                  stale: query.data.stale_after_seconds,
                })}
              </p>
              <StaticDataTable
                columns={columns}
                data={query.data.nodes}
                getRowKey={(node) => node.channel_id}
              />
            </>
          )}
          <Button
            variant='outline'
            disabled={query.isFetching}
            onClick={() => void query.refetch()}
          >
            {t('monitor.refresh')}
          </Button>
        </div>
      </CollapsibleContent>
    </Collapsible>
  )
}
