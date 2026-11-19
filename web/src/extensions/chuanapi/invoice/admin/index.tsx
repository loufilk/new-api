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

import { StaticDataTable } from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { SimpleRecordPager } from '@/features/agent-platform/components/simple-record-pager'
import { formatMinorMoney } from '@/features/agent-platform/lib/money'
import { formatTimestamp } from '@/lib/format'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { invoiceCategorySnapshotLabel } from '../contracts'
import { listInvoices, pageSize, reviewKey, type ReviewFilter } from './api'
import { DetailDialog } from './detail-dialog'
import { statusNames, useFinanceLabels } from './i18n'
import { SettingsDialog } from './settings-dialog'
import { ReviewStatus } from './status'

function ReviewQueue(props: { reviewer: number }) {
  const { label, locale } = useFinanceLabels()
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [status, setStatus] = useState<ReviewFilter>('pending')
  const [page, setPage] = useState(1)
  const [selected, setSelected] = useState<number | null>(null)
  const query = useQuery({
    queryKey: [...reviewKey, props.reviewer, 'list', status, page],
    queryFn: ({ signal }) => listInvoices(page, status, signal),
    gcTime: 0,
    staleTime: 0,
    retry: false,
    meta: { redirectOnInternalServerError: false },
  })
  const data = query.isError ? undefined : query.data

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {label('Invoice management', '开票管理')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='space-y-4'>
          <p className='text-muted-foreground text-sm'>
            {label(
              'Review applications after any required fee is paid. Record invoices already issued by finance; this console does not issue invoices or send email.',
              '应付手续费结清后方可审核开票。此处仅登记财务已开具的发票，不代开发票或发送邮件。'
            )}
          </p>
          <div className='flex flex-wrap items-center gap-2'>
            <Label htmlFor='finance-status'>
              {label('Application status', '申请状态')}
            </Label>
            <NativeSelect
              id='finance-status'
              value={status}
              onChange={(e) => {
                setPage(1)
                setStatus(e.target.value as ReviewFilter)
              }}
            >
              <NativeSelectOption value=''>
                {label('All statuses', '全部状态')}
              </NativeSelectOption>
              {Object.entries(statusNames).map(([value, names]) => (
                <NativeSelectOption key={value} value={value}>
                  {value === 'pending'
                    ? label('Pending & in progress', '待审核及开票中')
                    : label(...names)}
                </NativeSelectOption>
              ))}
              <NativeSelectOption value='fee_refund_review'>
                {label('Fee refund review', '手续费退款待人工核对')}
              </NativeSelectOption>
              <NativeSelectOption value='refund_review'>
                {label('Refund review', '退款待核对')}
              </NativeSelectOption>
            </NativeSelect>
            <Button
              className='ms-auto'
              size='sm'
              variant='outline'
              onClick={() => setSettingsOpen(true)}
            >
              {label('Invoice settings', '开票设置')}
            </Button>
            <Button
              size='sm'
              variant='outline'
              disabled={query.isFetching}
              onClick={() => void query.refetch()}
            >
              {label('Refresh', '刷新')}
            </Button>
          </div>
          {query.isPending && <LoadingState />}
          {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
          {data && (
            <>
              {data.items.length ? (
                <StaticDataTable
                  data={data.items}
                  getRowKey={(row) => row.id}
                  tableClassName='min-w-[700px]'
                  columns={[
                    {
                      id: 'id',
                      header: label('Application', '申请编号'),
                      cell: (row) => (
                        <Button
                          variant='link'
                          size='sm'
                          aria-label={`${label('View application', '查看申请')} #${row.id}`}
                          onClick={() => setSelected(row.id)}
                        >
                          #{row.id}
                        </Button>
                      ),
                    },
                    {
                      id: 'user',
                      header: label('User ID', '用户 ID'),
                      cell: (row) => row.user_id,
                    },
                    {
                      id: 'amount',
                      header: label('Invoice amount', '开票金额'),
                      cell: (row) =>
                        formatMinorMoney(
                          row.amount_minor,
                          row.currency,
                          2,
                          locale
                        ),
                    },
                    {
                      id: 'category',
                      header: label('Invoice category', '开票项目'),
                      cell: (row) => (
                        <span className='break-all whitespace-normal'>
                          {invoiceCategorySnapshotLabel(
                            row.category,
                            row.category_name,
                            label
                          )}
                        </span>
                      ),
                    },
                    {
                      id: 'orders',
                      header: label('Orders', '订单数'),
                      cell: (row) => row.order_count,
                    },
                    {
                      id: 'created',
                      header: label('Application date', '申请时间'),
                      cell: (row) => formatTimestamp(row.created_at),
                    },
                    {
                      id: 'status',
                      header: label('Status', '状态'),
                      cell: (row) => (
                        <ReviewStatus
                          status={row.status}
                          feeRefundReview={row.fee_state === 'refund_review'}
                          refundReview={row.requires_reconciliation}
                          processing={row.processing}
                          claimant={row.processing_by}
                          reviewer={props.reviewer}
                        />
                      ),
                    },
                  ]}
                />
              ) : (
                <EmptyState
                  bordered
                  title={label(
                    'No matching applications',
                    '没有符合条件的申请'
                  )}
                />
              )}
              <SimpleRecordPager
                page={page}
                total={data.total}
                pageSize={pageSize}
                onChange={setPage}
              />
            </>
          )}
          {settingsOpen && (
            <SettingsDialog
              reviewer={props.reviewer}
              onClose={() => setSettingsOpen(false)}
            />
          )}
          {selected != null && (
            <DetailDialog
              key={selected}
              id={selected}
              reviewer={props.reviewer}
              onClose={() => setSelected(null)}
              onSaved={() => {
                setSelected(null)
                setPage(1)
              }}
            />
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

export default function InvoiceManagement() {
  const user = useAuthStore((state) => state.auth.user)
  if (user?.role !== ROLE.SUPER_ADMIN) return null
  return <ReviewQueue key={user.id} reviewer={user.id} />
}
