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
import { useTranslation } from 'react-i18next'

import { formatMinorMoney } from '@/features/agent-platform/lib/money'
import { toIntlLocale } from '@/i18n/languages'

import type { InvoiceSummary } from '../contracts'

const chinese = {
  'For contact only. Invoices are not emailed automatically. Download issued invoices from My applications.':
    '邮箱仅用于联系，不会自动发送发票。开票后可在「我的申请」中下载。',
  Refresh: '刷新',
  'Refreshing…': '正在刷新…',
  'Order selection cleared. Review the refreshed list before submitting.':
    '订单选择已清空，请核对刷新后的订单再提交。',
  'Invoice center': '开票中心',
  'Available to invoice': '可开票金额',
  'Pending invoice amount': '开票中金额',
  'Issued invoice amount': '已开票金额',
  '{{n}} orders': '{{n}} 笔订单',
  '{{n}} applications': '{{n}} 笔申请',
  'Eligible orders': '可开票订单',
  'Select orders paid within the last 30 days. Up to 100 orders per application.':
    '可选择近 30 天内实付的订单，每次最多申请 100 笔。',
  'Orders not listed?': '找不到订单？',
  'Contact support for manual review.': '联系客服人工核对',
  'No eligible orders': '暂无可开票订单',
  'Select this page': '全选本页',
  'Selection limit reached. Deselect orders before adding more.':
    '每次最多选择 100 笔订单，请先取消部分勾选。',
  'Order number': '订单号',
  'Paid at': '支付时间',
  'Paid amount': '实付金额',
  'Invoice information': '发票信息',
  'Invoice type': '发票类型',
  'Ordinary invoice': '普通发票',
  'Special VAT invoice': '增值税专用发票',
  'Only company invoices are available. Enter the registered company name and tax ID.':
    '仅支持企业开票，请填写企业全称和纳税人识别号。',
  'Title type': '抬头类型',
  Company: '企业',
  Individual: '个人',
  'Invoice title': '发票抬头',
  'Tax ID': '纳税人识别号',
  'Invoice category': '开票类目',
  'Select an invoice category': '请选择开票类目',
  'Technical services': '技术服务费',
  'Software development': '软件开发费',
  'Email address': '联系邮箱',
  'Registered address': '注册地址',
  'Registered phone': '注册电话',
  'Bank name': '开户银行',
  'Bank account number': '银行账号',
  Required: '请填写此项',
  'Invalid field': '内容过长或含不支持的字符',
  'Enter a valid email address.': '请输入有效的邮箱地址',
  'Enter a 15–20 character tax ID using letters and digits.':
    '请输入 15 至 20 位字母或数字税号',
  'Enter a valid phone number.': '请输入有效的电话号码',
  'Enter the bank account using digits only.': '银行账号仅支持数字',
  'Special invoices require a company title.': '专用发票仅支持企业抬头。',
  'Selected orders': '已选订单',
  'Estimated invoice fee': '预计开票手续费',
  'Configured tax rate (reference only)': '配置的税率（仅供参考）',
  'Fee at application': '申请时的手续费',
  'Fee rate at application': '申请时的手续费率',
  'Configured tax rate at application': '申请时配置的税率',
  'This application requires a fee. It cannot be submitted until fee payment is available.':
    '此申请需支付开票手续费。手续费支付尚未开放，暂不能提交。',
  'This invoice type is currently unavailable': '该发票类型暂不受理',
  'Invoice amount': '开票金额',
  'Submit and choose payment': '提交并选择付款方式',
  'Submitting will not charge you. Choose a payment method in the application details.':
    '提交不会扣款。您可在申请详情中选择手续费支付方式。',
  'Awaiting fee payment': '待支付手续费',
  'Fee payment status': '手续费支付状态',
  Unpaid: '未支付',
  'Payment in progress': '支付处理中',
  Paid: '已支付',
  Refunded: '已退款',
  'Fee refund review': '手续费退款待核对',
  'Paid invoice fee': '实付手续费',
  'Fee paid at': '手续费支付时间',
  'Fee payment method': '手续费支付方式',
  'Choose a payment method': '选择支付方式',
  'Paid wallet balance': '付费余额',
  'Pay {{fee}} with {{method}}': '使用{{method}}支付 {{fee}}',
  'Use {{quota}} paid quota units to pay {{fee}}':
    '使用 {{quota}} 个付费额度单位支付 {{fee}}',
  'Only paid quota is used, not bonus or reward quota.':
    '仅使用付费额度，不使用赠金或奖励额度。',
  'Complete payment with the provider, then refresh the status here.':
    '请在支付平台完成付款，然后在此刷新支付状态。',
  'Refresh payment status': '刷新支付状态',
  'Check the payment status before trying again.': '请先核对支付状态，再重试。',
  'No payment method is currently available.': '暂无可用的手续费支付方式。',
  'If cancelled, the fee will be returned to your paid balance.':
    '撤销后，手续费将退回您的付费余额。',
  'If cancelled, your fee refund will require manual review.':
    '撤销后，手续费退款需要人工核对。',
  'Submit application': '提交开票申请',
  'Submitting…': '正在提交…',
  'Application submitted.': '开票申请已提交',
  'New applications are currently closed. You can still view and manage previous applications below.':
    '暂不受理新的开票申请，您仍可在下方查看和管理已有申请。',
  'My applications': '我的申请',
  'All statuses': '全部状态',
  'Application status': '申请状态',
  'Pending review': '待审核',
  'In progress': '开票中',
  'Invoice processing': '开票处理中',
  Issued: '已开票',
  Rejected: '已驳回',
  Cancelled: '已撤销',
  'No applications found': '暂无符合条件的开票申请',
  Application: '申请单',
  Details: '详情',
  'Application details': '开票申请详情',
  'Submitted at': '申请时间',
  'Issued at': '开票时间',
  'Included orders': '关联订单',
  'Invoice number': '发票号码',
  'Rejection reason': '驳回原因',
  'Download PDF': '下载 PDF 发票',
  'Cancel application': '撤销申请',
  'Cancel this application?': '确认撤销这笔开票申请？',
  'Eligible orders in this application will become available again.':
    '撤销后，仍符合条件的订单可以重新申请开票。',
  'Keep application': '保留申请',
  'Application cancelled.': '开票申请已撤销',
  'Previous page': '上一页',
  'Next page': '下一页',
  'Page {{page}} of {{pages}}': '第 {{page}} / {{pages}} 页',
  Retry: '重试',
  'Unable to load invoice information': '开票信息加载失败',
} as const

export type InvoiceTextKey = keyof typeof chinese
export function useInvoiceText() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const text = (
    key: InvoiceTextKey,
    vars?: Record<string, string | number>
  ): string =>
    t(key, {
      ...vars,
      defaultValue: locale?.startsWith('zh') ? chinese[key] : key,
      fallbackLng: false,
    })
  return {
    text,
    locale,
    money: (cents: string) => formatMinorMoney(cents, 'CNY', 2, locale),
  }
}

export const statusLabels = {
  awaiting_payment: 'Awaiting fee payment',
  pending: 'Pending review',
  issued: 'Issued',
  rejected: 'Rejected',
  cancelled: 'Cancelled',
} as const
export function applicationStatusLabel(entry: InvoiceSummary): InvoiceTextKey {
  if (entry.status === 'pending' && entry.processing) {
    return 'Invoice processing'
  }
  return statusLabels[entry.status]
}

export const invoiceTypeLabels = {
  ordinary: 'Ordinary invoice',
  special: 'Special VAT invoice',
} as const
export const titleTypeLabels = {
  company: 'Company',
  personal: 'Individual',
} as const
