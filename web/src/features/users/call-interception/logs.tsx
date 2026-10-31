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
import {
  getCoreRowModel,
  useReactTable,
  type PaginationState,
} from '@tanstack/react-table'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { DataTablePagination, DataTableView } from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldError,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { callInterceptionKey, getCallInterceptionLogs } from './api'
import { useInterceptionLogColumns } from './log-columns'
import type {
  InterceptionLog,
  InterceptionLogFilters,
  InterceptionLogKind,
} from './types'

const EMPTY_LOGS: InterceptionLog[] = []

type Props = { userId: number; kind: InterceptionLogKind }

export function CallInterceptionLogs(props: Props) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const prefix = useId()
  const [requestId, setRequestId] = useState('')
  const [identityId, setIdentityId] = useState('')
  const [callerId, setCallerId] = useState('')
  const [identityKind, setIdentityKind] = useState('all')
  const identityOptions = [
    { value: 'all', label: t('All identities') },
    { value: 'api_key', label: t('API key') },
    { value: 'dashboard_session', label: t('Dashboard session') },
    { value: 'artifact_capability', label: t('Artifact access capability') },
    { value: 'public_artifact', label: t('Public artifact access') },
  ]
  const [startTime, setStartTime] = useState('')
  const [endTime, setEndTime] = useState('')
  const [filterError, setFilterError] = useState('')
  const [filters, setFilters] = useState<
    Omit<InterceptionLogFilters, 'p' | 'page_size'>
  >({})
  const [pagination, setPagination] = useState<PaginationState>({
    pageIndex: 0,
    pageSize: 10,
  })
  const query = useQuery({
    queryKey: [
      ...callInterceptionKey(props.userId),
      props.kind,
      filters,
      pagination,
    ],
    queryFn: ({ signal }) =>
      getCallInterceptionLogs(
        props.userId,
        props.kind,
        {
          ...filters,
          p: pagination.pageIndex + 1,
          page_size: pagination.pageSize,
        },
        signal
      ),
    gcTime: 0,
  })
  const columns = useInterceptionLogColumns(props.kind)
  const table = useReactTable({
    columns,
    data: query.data?.items ?? EMPTY_LOGS,
    getCoreRowModel: getCoreRowModel(),
    manualPagination: true,
    rowCount: query.data?.total ?? 0,
    state: { pagination },
    onPaginationChange: setPagination,
  })

  function applyFilters(): void {
    const identity = identityId.trim() === '' ? undefined : Number(identityId)
    const caller = callerId.trim() === '' ? undefined : Number(callerId)
    const start = startTime
      ? Math.floor(new Date(startTime).getTime() / 1000)
      : undefined
    const end = endTime
      ? Math.floor(new Date(endTime).getTime() / 1000)
      : undefined
    if (
      (identity !== undefined &&
        (!Number.isSafeInteger(identity) || identity < 0)) ||
      (caller !== undefined && (!Number.isSafeInteger(caller) || caller < 0)) ||
      (start !== undefined && (!Number.isFinite(start) || start < 0)) ||
      (end !== undefined && (!Number.isFinite(end) || end < 0)) ||
      (start !== undefined && end !== undefined && start > end)
    ) {
      setFilterError(t('Enter a valid ID and time range'))
      return
    }
    setFilterError('')
    setFilters({
      request_id: requestId.trim() || undefined,
      actor_user_id: props.kind === 'hits' ? caller : undefined,
      identity_kind:
        props.kind === 'hits' && identityKind !== 'all'
          ? identityKind
          : undefined,
      ...(props.kind === 'hits'
        ? { token_id: identity }
        : { actor_id: identity }),
      start_time: start,
      end_time: end,
    })
    setPagination((current) => ({ ...current, pageIndex: 0 }))
  }

  return (
    <div className='flex min-w-0 flex-col gap-3 pt-3'>
      <p className='text-muted-foreground text-xs'>
        {t('User ID: {{id}}', { id: formatNumber(props.userId, locale) })}
        {' · '}
        {t(
          'These records are separate from usage and billing logs. No request body or secret key is stored.'
        )}
      </p>
      <FieldGroup
        className='grid gap-3 sm:grid-cols-2'
        onKeyDown={(event) => {
          if (
            event.key === 'Enter' &&
            event.target instanceof HTMLInputElement
          ) {
            event.preventDefault()
            applyFilters()
          }
        }}
      >
        <Field>
          <FieldLabel htmlFor={`${prefix}-request`}>
            {t('Request ID')}
          </FieldLabel>
          <Input
            id={`${prefix}-request`}
            value={requestId}
            onChange={(event) => setRequestId(event.target.value)}
            maxLength={128}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${prefix}-identity`}>
            {props.kind === 'hits' ? t('Key ID') : t('Administrator ID')}
          </FieldLabel>
          <Input
            id={`${prefix}-identity`}
            value={identityId}
            onChange={(event) => setIdentityId(event.target.value)}
            inputMode='numeric'
          />
        </Field>
        {props.kind === 'hits' && (
          <Field>
            <FieldLabel htmlFor={`${prefix}-caller`}>
              {t('Caller user ID')}
            </FieldLabel>
            <Input
              id={`${prefix}-caller`}
              value={callerId}
              onChange={(event) => setCallerId(event.target.value)}
              inputMode='numeric'
            />
          </Field>
        )}
        <Field>
          <FieldLabel htmlFor={`${prefix}-start`}>{t('Start time')}</FieldLabel>
          <Input
            id={`${prefix}-start`}
            type='datetime-local'
            value={startTime}
            onChange={(event) => setStartTime(event.target.value)}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${prefix}-end`}>{t('End time')}</FieldLabel>
          <Input
            id={`${prefix}-end`}
            type='datetime-local'
            value={endTime}
            onChange={(event) => setEndTime(event.target.value)}
          />
        </Field>
        {props.kind === 'hits' && (
          <Field className='sm:col-span-2'>
            <FieldLabel htmlFor={`${prefix}-kind`}>
              {t('Request identity')}
            </FieldLabel>
            <Select
              value={identityKind}
              onValueChange={(value) =>
                value !== null && setIdentityKind(value)
              }
              items={identityOptions}
            >
              <SelectTrigger id={`${prefix}-kind`}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  {identityOptions.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {option.label}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          </Field>
        )}
      </FieldGroup>
      <FieldError>{filterError}</FieldError>
      <div className='flex flex-wrap gap-2'>
        <Button type='button' variant='outline' onClick={applyFilters}>
          {t('Apply filters')}
        </Button>
        <Button
          type='button'
          variant='ghost'
          onClick={() => {
            setRequestId('')
            setIdentityId('')
            setCallerId('')
            setIdentityKind('all')
            setStartTime('')
            setEndTime('')
            setFilterError('')
            setFilters({})
            setPagination((current) => ({ ...current, pageIndex: 0 }))
          }}
        >
          {t('Reset filters')}
        </Button>
        <Button
          type='button'
          variant='outline'
          disabled={query.isFetching}
          onClick={() => {
            void query.refetch()
          }}
        >
          {query.isFetching ? t('Loading...') : t('Refresh logs')}
        </Button>
      </div>
      {query.isError ? (
        <ErrorState
          title={t('Failed to load interception logs')}
          onRetry={() => {
            void query.refetch()
          }}
          className='min-h-32'
        />
      ) : (
        <>
          <DataTableView
            table={table}
            isLoading={query.isPending}
            emptyTitle={t('No interception records')}
            tableClassName='min-w-[680px]'
            tableContainerClassName='overflow-x-auto'
            containerProps={{
              role: 'region',
              'aria-label': t('Interception logs'),
              tabIndex: 0,
            }}
          />
          <fieldset disabled={query.isFetching}>
            <DataTablePagination table={table} compact />
          </fieldset>
        </>
      )}
    </div>
  )
}
