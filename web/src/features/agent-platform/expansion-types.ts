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
export interface ExpansionPolicy {
  agreement_minimum_rate_bps: number
  governance_enabled: boolean
  governance_required_for_activation: boolean
  credit_governance_required: boolean
  governance_inactivity_seconds: number
  credit_enabled: boolean
  credit_terms_approved: boolean
  credit_self_use_enabled: boolean
  credit_maximum_limit_minor: string
  credit_term_seconds: number
  credit_maximum_term_seconds: number
  credit_quota_numerator: number
  credit_quota_denominator: number
  credit_rate_bps: number
  deposit_enabled: boolean
  deposit_terms_approved: boolean
  deposit_offset_enabled: boolean
  deposit_refund_enabled: boolean
  deposit_maximum_minor: string
  campaign_enabled: boolean
  campaign_terms_approved: boolean
  agreement_pricing_enabled: boolean
  manual_reconciliation_enabled: boolean
  credit_recognition_mode: string
  credit_interest_mode: string
  credit_currency: string
  credit_currency_exponent: number
  deposit_currency: string
  deposit_currency_exponent: number
}
