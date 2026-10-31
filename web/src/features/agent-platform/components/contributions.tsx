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
import type { ColumnDef } from '@tanstack/react-table'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { DataTablePage, useDataTable } from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'

import { usePlatformList } from '../hooks/use-platform-list'
import { ActionForm } from './action-form'
import { BusinessDate, Money } from './values'

interface ContributionCurrency {
  currency: string
  currency_exponent: number
  confirmed_topup_principal_minor: string
  refunded_principal_minor: string
  net_topup_principal_minor: string
  commission_earned_minor: string
  commission_reversed_minor: string
  commission_net_minor: string
}
interface Contribution {
  customer_user_id: number
  display_name: string
  currencies: ContributionCurrency[]
}
interface ContributionCoverage {
  coverage: {
    scope: string
    historical_complete: boolean
    commission_cutover_at: number
    earliest_verified_receipt_at: number
  }
}
export function ContributionsPanel(props: { finance?: boolean }) {
  const { t } = useTranslation()
  const [target, setTarget] = useState<number | undefined>()
  const [selecting, setSelecting] = useState(false)
  const { query, pagination, setPagination } = usePlatformList<
    Contribution,
    ContributionCoverage
  >(
    props.finance ? '/finance/contributions' : '/contributions',
    { agent_user_id: target },
    !props.finance || !!target
  )
  const columns: ColumnDef<Contribution>[] = [
    {
      accessorKey: 'customer_user_id',
      header: t('Referred customer ID'),
      meta: { mobileTitle: true },
    },
    { accessorKey: 'display_name', header: t('Display Name') },
    {
      id: 'currencies',
      header: t('Verified contribution by currency'),
      cell: ({ row }) => (
        <div className='flex flex-col gap-4'>
          {row.original.currencies.length ? (
            row.original.currencies.map((currency) => (
              <dl
                key={currency.currency}
                className='grid grid-cols-1 gap-2 text-sm sm:grid-cols-2'
              >
                {(
                  [
                    [
                      'confirmed_topup_principal_minor',
                      'Confirmed top-up principal',
                    ],
                    ['refunded_principal_minor', 'Refunded top-up principal'],
                    [
                      'net_topup_principal_minor',
                      'Net confirmed top-up principal',
                    ],
                    ['commission_earned_minor', 'Commission earned'],
                    ['commission_reversed_minor', 'Commission reversed'],
                    ['commission_net_minor', 'Net commission contribution'],
                  ] as const
                ).map(([key, label]) => (
                  <div key={key}>
                    <dt className='text-muted-foreground'>{t(label)}</dt>
                    <dd>
                      <Money
                        minor={currency[key]}
                        currency={currency.currency}
                        exponent={currency.currency_exponent}
                      />
                    </dd>
                  </div>
                ))}
              </dl>
            ))
          ) : (
            <span>{t('No verified receipt history')}</span>
          )}
        </div>
      ),
    },
  ]
  const { table } = useDataTable({
    data: query.data?.items ?? [],
    columns,
    pagination,
    onPaginationChange: setPagination,
    manualPagination: true,
    totalCount: query.data?.total ?? 0,
    enableSorting: false,
  })
  return (
    <div className='flex flex-col gap-4'>
      <Alert>
        <AlertDescription>
          {t(
            'Contributions include verified live top-up receipts only. Historical coverage may be incomplete. Currencies are kept separate; no exchange rate or unverified historical total is inferred.'
          )}
        </AlertDescription>
      </Alert>
      {props.finance && (
        <div className='flex flex-wrap items-center gap-3'>
          <Button variant='outline' onClick={() => setSelecting(true)}>
            {t('Find user')}
          </Button>
          {target && (
            <span>
              {t('User ID')}: {target}
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
            setPagination({ pageIndex: 0, pageSize: pagination.pageSize })
            setSelecting(false)
          }}
        />
      )}
      {query.data?.coverage && (
        <dl className='grid grid-cols-1 gap-3 text-sm sm:grid-cols-2'>
          <div>
            <dt>{t('Earliest verified receipt')}</dt>
            <dd>
              <BusinessDate
                value={query.data.coverage.earliest_verified_receipt_at}
              />
            </dd>
          </div>
          <div>
            <dt>{t('Commission cutover')}</dt>
            <dd>
              <BusinessDate value={query.data.coverage.commission_cutover_at} />
            </dd>
          </div>
        </dl>
      )}
      {query.isError ? (
        <ErrorState
          onRetry={() => {
            void query.refetch()
          }}
        />
      ) : (
        <DataTablePage
          table={table}
          columns={columns}
          isLoading={query.isPending && (!props.finance || !!target)}
          isFetching={query.isFetching}
          emptyTitle={
            props.finance && !target
              ? t('Select an account to review its financial records.')
              : t('No records found')
          }
          toolbar={null}
          paginationInFooter={false}
        />
      )}
    </div>
  )
}
