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
import { currencyOptions } from '../lib/currencies'
import type { ActionField } from './action-form'

export const expansionPolicyFields: ActionField[] = [
  {
    name: 'agreement_minimum_rate_bps',
    label: 'Minimum approved agreement price (basis points)',
    kind: 'integer',
    max: 10000,
  },
  {
    name: 'governance_enabled',
    label: 'Enable qualification dossiers',
    kind: 'checkbox',
  },
  {
    name: 'governance_required_for_activation',
    label: 'Require qualification before agent activation',
    kind: 'checkbox',
  },
  {
    name: 'credit_governance_required',
    label: 'Require current qualification for new credit',
    kind: 'checkbox',
  },
  {
    name: 'governance_inactivity_seconds',
    label: 'Inactivity review threshold (seconds)',
    kind: 'integer',
  },
  { name: 'credit_enabled', label: 'Enable new credit', kind: 'checkbox' },
  {
    name: 'credit_terms_approved',
    label: 'Credit terms approved',
    kind: 'checkbox',
  },
  {
    name: 'credit_self_use_enabled',
    label: 'Enable credit self-use',
    kind: 'checkbox',
  },
  {
    name: 'credit_maximum_limit_minor',
    label: 'Maximum credit limit (minor units)',
    kind: 'minor',
  },
  {
    name: 'credit_term_seconds',
    label: 'Default credit term (seconds)',
    kind: 'integer',
  },
  {
    name: 'credit_maximum_term_seconds',
    label: 'Maximum credit term (seconds)',
    kind: 'integer',
  },
  {
    name: 'credit_quota_numerator',
    label: 'Credit quota conversion numerator',
    kind: 'integer',
  },
  {
    name: 'credit_quota_denominator',
    label: 'Credit quota conversion denominator',
    kind: 'integer',
  },
  {
    name: 'credit_rate_bps',
    label: 'Credit price (basis points)',
    kind: 'integer',
    max: 10000,
  },
  { name: 'deposit_enabled', label: 'Enable new deposits', kind: 'checkbox' },
  {
    name: 'deposit_terms_approved',
    label: 'Deposit terms approved',
    kind: 'checkbox',
  },
  {
    name: 'deposit_offset_enabled',
    label: 'Enable reviewed deposit offsets',
    kind: 'checkbox',
  },
  {
    name: 'deposit_refund_enabled',
    label: 'Enable reviewed deposit refunds',
    kind: 'checkbox',
  },
  {
    name: 'deposit_maximum_minor',
    label: 'Maximum deposit (minor units)',
    kind: 'minor',
  },
  {
    name: 'campaign_enabled',
    label: 'Enable approved campaigns',
    kind: 'checkbox',
  },
  {
    name: 'campaign_terms_approved',
    label: 'Campaign terms approved',
    kind: 'checkbox',
  },
  {
    name: 'agreement_pricing_enabled',
    label: 'Enable approved agreement pricing',
    kind: 'checkbox',
  },
  {
    name: 'manual_reconciliation_enabled',
    label: 'Enable manual bank reconciliation',
    kind: 'checkbox',
  },
  {
    name: 'credit_recognition_mode',
    label: 'Receivable recognition rule',
    kind: 'select',
    required: false,
    options: [
      {
        value: 'source_spend',
        label: 'Recognize when credit-funded quota is spent',
      },
    ],
  },
  {
    name: 'credit_interest_mode',
    label: 'Credit interest rule',
    kind: 'select',
    required: false,
    options: [{ value: 'none', label: 'No interest or compounding' }],
  },
  {
    name: 'credit_currency',
    label: 'Credit currency (ISO code)',
    kind: 'select',
    required: false,
    options: currencyOptions('credit_currency_exponent'),
  },
  {
    name: 'credit_currency_exponent',
    label: 'Credit currency decimal places',
    kind: 'integer',
    readOnly: true,
    max: 6,
  },
  {
    name: 'deposit_currency',
    label: 'Deposit currency (ISO code)',
    kind: 'select',
    required: false,
    options: currencyOptions('deposit_currency_exponent'),
  },
  {
    name: 'deposit_currency_exponent',
    label: 'Deposit currency decimal places',
    kind: 'integer',
    readOnly: true,
    max: 6,
  },
]
