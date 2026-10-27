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
import type { BillingSettings } from '@/features/system-settings/types'

import type { PaymentSettingsExtension } from '../../payment-settings'
import { PaymentSettingsSection } from './payment-settings-section'

function renderChuanApiPaymentSettings(settings: BillingSettings) {
  return (
    <PaymentSettingsSection
      defaultValues={{
        PayAddress: settings.PayAddress,
        EpayId: settings.EpayId,
        EpayKey: settings.EpayKey,
        Price: settings.Price,
        MinTopUp: settings.MinTopUp,
        CustomCallbackAddress: settings.CustomCallbackAddress,
        PayMethods: settings.PayMethods,
        AmountOptions: settings['payment_setting.amount_options'],
        AmountDiscount: settings['payment_setting.amount_discount'],
        StripeApiSecret: settings.StripeApiSecret,
        StripeWebhookSecret: settings.StripeWebhookSecret,
        StripePriceId: settings.StripePriceId,
        StripeUnitPrice: settings.StripeUnitPrice,
        StripeMinTopUp: settings.StripeMinTopUp,
        StripePromotionCodesEnabled: settings.StripePromotionCodesEnabled,
        CreemApiKey: settings.CreemApiKey,
        CreemWebhookSecret: settings.CreemWebhookSecret,
        CreemTestMode: settings.CreemTestMode,
        CreemProducts: settings.CreemProducts,
      }}
      waffoDefaultValues={{
        WaffoEnabled: settings.WaffoEnabled ?? false,
        WaffoApiKey: settings.WaffoApiKey ?? '',
        WaffoPrivateKey: settings.WaffoPrivateKey ?? '',
        WaffoPublicCert: settings.WaffoPublicCert ?? '',
        WaffoSandboxPublicCert: settings.WaffoSandboxPublicCert ?? '',
        WaffoSandboxApiKey: settings.WaffoSandboxApiKey ?? '',
        WaffoSandboxPrivateKey: settings.WaffoSandboxPrivateKey ?? '',
        WaffoSandbox: settings.WaffoSandbox ?? false,
        WaffoMerchantId: settings.WaffoMerchantId ?? '',
        WaffoCurrency: settings.WaffoCurrency ?? 'USD',
        WaffoUnitPrice: settings.WaffoUnitPrice ?? 1,
        WaffoMinTopUp: settings.WaffoMinTopUp ?? 1,
        WaffoNotifyUrl: settings.WaffoNotifyUrl ?? '',
        WaffoReturnUrl: settings.WaffoReturnUrl ?? '',
        WaffoPayMethods: settings.WaffoPayMethods ?? '[]',
      }}
      waffoPancakeDefaultValues={{
        WaffoPancakeMerchantID: settings.WaffoPancakeMerchantID ?? '',
        WaffoPancakePrivateKey: settings.WaffoPancakePrivateKey ?? '',
        WaffoPancakeEnvironment: settings.WaffoPancakeEnvironment ?? 'prod',
        WaffoPancakeReturnURL: settings.WaffoPancakeReturnURL ?? '',
      }}
      waffoPancakeProvisionedStoreID={settings.WaffoPancakeStoreID ?? ''}
      waffoPancakeProvisionedProductID={settings.WaffoPancakeProductID ?? ''}
      complianceDefaults={{
        confirmed: settings['payment_setting.compliance_confirmed'] ?? false,
        termsVersion:
          settings['payment_setting.compliance_terms_version'] ?? '',
        confirmedAt: settings['payment_setting.compliance_confirmed_at'] ?? 0,
        confirmedBy: settings['payment_setting.compliance_confirmed_by'] ?? 0,
      }}
    />
  )
}

export const chuanapiPaymentSettingsExtension: PaymentSettingsExtension = {
  id: 'chuanapi/payment-settings',
  render: (settings) =>
    renderChuanApiPaymentSettings(settings as BillingSettings),
}
