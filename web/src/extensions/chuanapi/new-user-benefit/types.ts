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
export interface BenefitPaymentMethod {
  type: string
  name: string
  icon?: string
}

export interface NewUserBenefitConfig {
  enabled: boolean
  pay_amount_cny: string
  credit_amount_cny: string
  version: number
}

export interface BenefitOrder {
  order_id: string
  pay_amount_cny: string
  credit_amount_cny: string
  payment_method: string
  config_version: number
}

export interface NewUserBenefitInfo {
  enabled: boolean
  eligible: boolean
  status: 'available' | 'pending' | 'claimed' | 'ineligible'
  pay_amount_cny: string
  credit_amount_cny: string
  paid_quota: number
  bonus_quota: number
  config_version: number
  pay_methods: BenefitPaymentMethod[]
  pending_order?: BenefitOrder
}

export interface BenefitOffer {
  payAmount: string
  creditAmount: string
  version: number
  methods: BenefitPaymentMethod[]
  orderId?: string
}

export interface BenefitPaymentRequest {
  payment_method: string
  config_version: number
}

export interface BenefitCheckout {
  order_id: string
  pay_amount_cny: string
  credit_amount_cny: string
  status: 'pending'
  checkout: {
    kind: 'form'
    url: string
    params: Record<string, string>
  }
}
