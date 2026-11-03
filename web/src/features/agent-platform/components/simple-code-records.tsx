/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { formatTimestampToDate } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { postPlatform } from '../api'
import { useRequestId } from '../hooks/use-request-id'
import { formatAgentQuota as formatQuota } from '../lib/money'
import {
  cancelSimpleCode,
  getSimpleAgentCodes,
  type SimpleCodeRecord,
} from '../simple-api'
import { ActionForm } from './action-form'
import { SimpleRecordPager } from './simple-record-pager'

function isExpired(row: SimpleCodeRecord) {
  return (
    row.status === 1 && row.expired_at > 0 && row.expired_at < Date.now() / 1000
  )
}

export function SimpleCodeRecords(props: {
  agentUserId?: number
  mode?: 'all' | 'direct' | 'payment'
  hideFilter?: boolean
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [selectedMode, setMode] = useState<'all' | 'direct' | 'payment'>('all')
  const mode = props.mode ?? selectedMode
  const [page, setPage] = useState(1)
  const query = useQuery({
    queryKey: ['agent-platform', 'simple-codes', props.agentUserId, mode, page],
    queryFn: () =>
      getSimpleAgentCodes({
        agent_user_id: props.agentUserId,
        mode: mode === 'all' ? undefined : mode,
        p: page,
        page_size: 20,
      }),
  })
  if (query.isPending) return <LoadingState />
  if (query.isError) {
    return (
      <ErrorState
        onRetry={() => {
          void query.refetch()
        }}
      />
    )
  }
  return (
    <div className='space-y-3'>
      {!props.hideFilter && (
        <ModeFilter
          mode={mode}
          onChange={(value) => {
            setMode(value)
            setPage(1)
          }}
        />
      )}
      {query.data.items.length ? (
        <div className='divide-border overflow-hidden rounded-lg border'>
          {query.data.items.map((row) => (
            <SimpleCodeRecordRow
              key={row.id}
              row={row}
              onChanged={() => {
                void client.invalidateQueries({ queryKey: ['agent-platform'] })
              }}
            />
          ))}
        </div>
      ) : (
        <EmptyState
          title={t('No redemption codes found')}
          description={t('Issued direct and credit codes will appear here.')}
        />
      )}
      <SimpleRecordPager
        page={page}
        total={query.data.total}
        onChange={setPage}
      />
    </div>
  )
}

function ModeFilter(props: {
  mode: 'all' | 'direct' | 'payment'
  onChange: (mode: 'all' | 'direct' | 'payment') => void
}) {
  const { t } = useTranslation()
  return (
    <div className='flex items-center justify-between gap-3'>
      <h2 className='text-sm font-semibold'>{t('Redemption code records')}</h2>
      <NativeSelect
        value={props.mode}
        onChange={(event) =>
          props.onChange(event.target.value as 'all' | 'direct' | 'payment')
        }
        className='w-40'
        aria-label={t('Filter code issue method')}
      >
        <NativeSelectOption value='all'>{t('All methods')}</NativeSelectOption>
        <NativeSelectOption value='direct'>
          {t('Direct issue')}
        </NativeSelectOption>
        <NativeSelectOption value='payment'>
          {t('Customer payment')}
        </NativeSelectOption>
      </NativeSelect>
    </div>
  )
}

function SimpleCodeRecordRow(props: {
  row: SimpleCodeRecord
  onChanged: () => void
}) {
  const { t } = useTranslation()
  const [busy, setBusy] = useState(false)
  const [cancelOpen, setCancelOpen] = useState(false)
  const requestId = useRequestId()
  const root = useAuthStore(
    (state) => state.auth.user?.role === ROLE.SUPER_ADMIN
  )
  const [revealOpen, setRevealOpen] = useState(false)
  const [revealedKey, setRevealedKey] = useState('')
  const canCancel =
    props.row.mode !== 'payment' &&
    props.row.status === 1 &&
    !isExpired(props.row) &&
    !props.row.redeemed_at
  let status = t('Used')
  if (isExpired(props.row)) status = t('Expired')
  else if (props.row.status === 1) status = t('Unused')
  else if (props.row.status === 2) status = t('Disabled')
  const cancel = async () => {
    if (!canCancel || busy) return
    setBusy(true)
    try {
      await cancelSimpleCode(props.row.id, {
        request_id: requestId({
          code_id: props.row.id,
          version: props.row.version,
        }),
        version: props.row.version,
        reason: 'Agent cancelled an undelivered code',
      })
      setCancelOpen(false)
      props.onChanged()
    } catch (error) {
      handleServerError(error)
    } finally {
      setBusy(false)
    }
  }
  return (
    <>
      <div className='flex flex-wrap items-center gap-3 border-b p-3 last:border-b-0'>
        <div className='min-w-44 flex-1'>
          <div className='font-medium'>{props.row.name}</div>
          <div className='text-muted-foreground text-xs'>
            {formatTimestampToDate(props.row.created_at)} - {t('Agent')} #
            {props.row.agent_user_id}
          </div>
        </div>
        <div className='text-right'>
          <div className='font-medium tabular-nums'>
            {formatQuota(props.row.quota)}
          </div>
          <div className='text-muted-foreground text-xs'>
            {props.row.mode === 'payment'
              ? t('Customer payment')
              : t('Direct issue')}
          </div>
        </div>
        <StatusBadge
          label={status}
          variant={
            props.row.status === 1 && !isExpired(props.row)
              ? 'success'
              : 'neutral'
          }
          copyable={false}
        />
        <div className='flex min-w-56 items-center gap-1'>
          <code className='min-w-0 flex-1 truncate text-xs'>
            {revealedKey || props.row.key || props.row.masked_key}
          </code>
          {(revealedKey || props.row.key) && (
            <CopyButton
              value={revealedKey || props.row.key || ''}
              aria-label={t('Copy redemption code')}
            />
          )}
        </div>
        {root && !revealedKey && (
          <Button
            size='sm'
            variant='outline'
            onClick={() => setRevealOpen(true)}
          >
            {t('View full code')}
          </Button>
        )}
        {canCancel && (
          <Button
            size='sm'
            variant='outline'
            disabled={busy}
            onClick={() => setCancelOpen(true)}
          >
            {t('Cancel code')}
          </Button>
        )}
      </div>
      {revealOpen && (
        <ActionForm
          title={t('View full code')}
          fields={[
            { name: 'reason', label: 'Reason', kind: 'textarea', max: 255 },
          ]}
          onClose={() => setRevealOpen(false)}
          onSubmit={async (values) => {
            const result = await postPlatform<{ key: string }>(
              `/simple/finance/codes/${props.row.id}/reveal`,
              { reason: String(values.reason) }
            )
            setRevealedKey(result.key)
            setRevealOpen(false)
          }}
        />
      )}
      <ConfirmDialog
        open={cancelOpen}
        onOpenChange={setCancelOpen}
        title={t('Cancel this redemption code?')}
        desc={t(
          'Only cancel if this code has not been given to the customer or the customer has agreed. The reserved agent balance will be returned.'
        )}
        destructive
        isLoading={busy}
        confirmText={t('Cancel code')}
        handleConfirm={() => void cancel()}
      />
    </>
  )
}
