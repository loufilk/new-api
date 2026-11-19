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
import { useEffect, useRef, useState } from 'react'

import { StaticDataTable } from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { formatMinorMoney } from '@/features/agent-platform/lib/money'
import { formatTimestamp } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'

import { downloadInvoiceDocument } from '../contracts'
import {
  getInvoice,
  reviewKey,
  type AdminDetail,
  type ReviewDecision,
} from './api'
import { ClaimDialog } from './claim-dialog'
import { DecisionDialog } from './decision-dialog'
import { financeFeeCleared, feeFacts } from './fee-review'
import { useFinanceLabels } from './i18n'
import { ReviewStatus } from './status'

function InvoiceFacts(props: { detail: AdminDetail; reviewer: number }) {
  const { label, locale } = useFinanceLabels()
  const inv = props.detail
  const facts = [
    [label('User ID', '用户 ID'), inv.user_id],
    [
      label('Invoice type', '发票类型'),
      inv.invoice_type === 'special'
        ? label('Special VAT invoice', '增值税专用发票')
        : label('Ordinary invoice', '普通发票'),
    ],
    [
      label('Title type', '抬头类型'),
      inv.title_type === 'company'
        ? label('Company', '企业')
        : label('Individual', '个人'),
    ],
    [label('Invoice title', '发票抬头'), inv.title],
    [label('Tax ID', '纳税人识别号'), inv.tax_id],
    [
      label('Invoice category', '开票项目'),
      inv.category === 'technical_service'
        ? label('Technical services', '技术服务')
        : label('Software development', '软件开发'),
    ],
    ...feeFacts(inv, label, locale),
    [label('Email', '邮箱'), inv.email],
    [label('Address', '地址'), inv.address],
    [label('Phone', '电话'), inv.phone],
    [label('Bank name', '开户银行'), inv.bank_name],
    [label('Bank account', '银行账户'), inv.bank_account],
    [label('Invoice number', '发票号码'), inv.invoice_number],
    [label('Application date', '申请时间'), formatTimestamp(inv.created_at)],
    [label('Updated at', '更新时间'), formatTimestamp(inv.updated_at)],
    [
      label('Recorded at', '登记时间'),
      inv.issued_at ? formatTimestamp(inv.issued_at) : null,
    ],
    [label('Reason for rejection', '驳回原因'), inv.reason],
    [
      label('Internal review reason', '内部核对原因'),
      inv.reconciliation_reason,
    ],
  ]
  return (
    <div className='space-y-5'>
      <div className='flex flex-wrap items-center justify-between gap-2 rounded-lg border p-3'>
        <div>
          <p className='text-muted-foreground text-xs'>
            {label('Invoice amount', '开票金额')}
          </p>
          <strong className='text-lg tabular-nums'>
            {formatMinorMoney(inv.amount_minor, inv.currency, 2, locale)}
          </strong>
        </div>
        <ReviewStatus
          status={inv.status}
          feeRefundReview={inv.fee_state === 'refund_review'}
          refundReview={inv.requires_reconciliation}
          processing={inv.processing}
          claimant={inv.processing_by}
          reviewer={props.reviewer}
        />
      </div>
      {inv.fee_state === 'refund_review' && (
        <Alert>
          <AlertDescription>
            {label(
              'Review the Epay fee with the payment provider. No automatic cash refund has been sent.',
              '请与支付平台核对易支付手续费的退款情况。系统未自动原路退款。'
            )}
          </AlertDescription>
        </Alert>
      )}
      <dl className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
        {facts
          .filter(([, v]) => v != null && v !== '')
          .map(([name, value]) => (
            <div key={String(name)} className='min-w-0'>
              <dt className='text-muted-foreground text-xs'>{name}</dt>
              <dd className='text-sm break-all'>{value}</dd>
            </div>
          ))}
      </dl>
      <section className='space-y-2'>
        <h3 className='text-sm font-semibold'>
          {label('Included payments', '关联实付订单')} ({inv.order_count})
        </h3>
        <StaticDataTable
          data={inv.orders}
          getRowKey={(row) => row.id}
          columns={[
            {
              id: 'reference',
              header: label('Order reference', '支付订单号'),
              cell: (row) => row.reference,
            },
            {
              id: 'paid',
              header: label('Paid at', '支付时间'),
              cell: (row) => formatTimestamp(row.paid_at),
            },
            {
              id: 'amount',
              header: label('Paid amount', '实付金额'),
              cell: (row) =>
                formatMinorMoney(row.amount_minor, row.currency, 2, locale),
            },
          ]}
        />
      </section>
    </div>
  )
}

export function DetailDialog(props: {
  id: number
  reviewer: number
  onClose: () => void
  onSaved: () => void
}) {
  const { label } = useFinanceLabels()
  const [decision, setDecision] = useState<ReviewDecision['kind'] | null>(null)
  const [downloading, setDownloading] = useState(false)
  const downloadController = useRef<AbortController | null>(null)
  useEffect(() => () => downloadController.current?.abort(), [])
  const query = useQuery({
    queryKey: [...reviewKey, props.reviewer, 'detail', props.id],
    queryFn: ({ signal }) => getInvoice(props.id, signal),
    gcTime: 0,
    staleTime: 0,
    retry: false,
    meta: { redirectOnInternalServerError: false },
  })
  const inv = query.isError ? undefined : query.data
  async function download() {
    if (
      downloadController.current ||
      !inv?.has_document ||
      inv.status !== 'issued'
    ) {
      return
    }
    const controller = new AbortController()
    downloadController.current = controller
    setDownloading(true)
    try {
      await downloadInvoiceDocument(inv.id, true, controller.signal)
    } catch (error) {
      if (!controller.signal.aborted) handleServerError(error)
    } finally {
      if (!controller.signal.aborted) setDownloading(false)
      downloadController.current = null
    }
  }
  const pending = inv?.status === 'pending'
  const flaggable = inv?.status === 'issued' && !inv.requires_reconciliation
  const feeCleared = inv != null && financeFeeCleared(inv)
  const unclaimed =
    pending && feeCleared && !inv.processing && !inv.processing_by
  const rejectable =
    unclaimed ||
    (inv?.status === 'awaiting_payment' &&
      inv.fee_state === 'unpaid' &&
      !inv.processing &&
      !inv.processing_by &&
      !inv.fee_rail &&
      inv.fee_paid_minor === '0' &&
      !inv.fee_paid_at)
  const owned =
    pending && inv.processing === true && inv.processing_by === props.reviewer
  const corporate = inv?.title_type === 'company'
  const permitted: Record<ReviewDecision['kind'], boolean> = {
    claim: unclaimed && corporate,
    reject: rejectable,
    release: owned,
    record: owned && feeCleared && corporate,
    reconciliation: flaggable,
  }
  const canDecide = decision !== null && permitted[decision]
  return (
    <>
      <Dialog
        open
        onOpenChange={(open) => {
          if (!open && !(decision && canDecide)) props.onClose()
        }}
        title={`${label('Invoice application', '开票申请')} #${props.id}`}
        contentClassName='max-h-[90dvh]'
        footer={
          <>
            <Button variant='outline' onClick={props.onClose}>
              {label('Close', '关闭')}
            </Button>
            {rejectable && (
              <Button variant='outline' onClick={() => setDecision('reject')}>
                {label('Reject application', '驳回申请')}
              </Button>
            )}
            {permitted.claim && (
              <Button onClick={() => setDecision('claim')}>
                {label('Claim for issuance', '开始开票')}
              </Button>
            )}
            {owned && (
              <>
                <Button
                  variant='outline'
                  onClick={() => setDecision('release')}
                >
                  {label('Release claim', '释放开票占用')}
                </Button>
                {permitted.record && (
                  <Button onClick={() => setDecision('record')}>
                    {label('Record issued PDF', '登记已开具的 PDF')}
                  </Button>
                )}
              </>
            )}
            {flaggable && (
              <Button
                variant='outline'
                onClick={() => setDecision('reconciliation')}
              >
                {label('Flag for refund reconciliation', '标记退款待核对')}
              </Button>
            )}
            {inv?.status === 'issued' && inv.has_document && (
              <Button disabled={downloading} onClick={() => void download()}>
                {label('Download PDF', '下载 PDF')}
              </Button>
            )}
          </>
        }
      >
        {query.isPending && <LoadingState />}
        {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
        {inv && <InvoiceFacts detail={inv} reviewer={props.reviewer} />}
      </Dialog>
      {(decision === 'claim' || decision === 'release') && canDecide && (
        <ClaimDialog
          id={props.id}
          kind={decision}
          onCancel={() => setDecision(null)}
          onSaved={() => setDecision(null)}
        />
      )}
      {decision &&
        decision !== 'claim' &&
        decision !== 'release' &&
        canDecide && (
          <DecisionDialog
            id={props.id}
            kind={decision}
            paidRail={inv?.fee_state === 'paid' ? inv.fee_rail : undefined}
            onCancel={() => setDecision(null)}
            onSaved={props.onSaved}
          />
        )}
    </>
  )
}
