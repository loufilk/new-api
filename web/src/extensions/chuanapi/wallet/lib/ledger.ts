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
// 账本 reason 到 i18n 文案键的映射。未知 reason 原样展示（后端兼容旧数据）。
const REASON_LABEL_KEYS: Record<string, string> = {
  topup: 'Top-up',
  admin_topup: 'Admin Top-up',
  redemption: 'Redemption',
  checkin: 'Check-in',
  invitee_bonus: 'Invitee Gift',
  admin_add: 'Admin Added',
  admin_subtract: 'Admin Deducted',
  admin_override: 'Admin Override',
  subscription_purchase: 'Subscription Purchase',
  billing_pre_consume: 'Billing Pre-consumed',
  billing_reserve: 'Billing Reserve',
  billing_settle: 'Billing Settlement',
  billing_refund: 'Billing Refund',
  task_settle: 'Task Settlement',
  task_refund: 'Task Refund',
  new_user_gift: 'New User Gift',
  signup_bonus: 'Signup Bonus',
  referral_transfer: 'Referral Reward Transfer',
  legacy_backfill: 'Legacy Backfill',
  rc24_reconcile: 'RC24 Usage Reconciliation',
  system: 'System',
}

export function ledgerReasonLabelKey(reason: string): string {
  return REASON_LABEL_KEYS[reason] ?? reason
}
