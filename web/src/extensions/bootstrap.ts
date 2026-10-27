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
import { setBrandExtension } from './brand'
import {
  chuanapiBrandExtension,
  chuanapiHelpExtension,
  chuanapiI18nExtension,
  chuanapiNavigationExtension,
  chuanapiPaymentSettingsExtension,
  chuanapiWalletExtension,
  chuanapiMonitorExtension,
  chuanapiMonitorNavigationExtension,
  SHOW_LEGACY_AFFILIATE_QUOTA,
} from './chuanapi'
import { setExtensionFlags } from './flags'
import { setHelpExtension } from './help'
import { setI18nExtensions } from './i18n'
import { setMonitorExtension } from './monitor'
import { setNavigationExtensions } from './navigation'
import { setPaymentSettingsExtension } from './payment-settings'
import { setWalletExtension } from './wallet'

// Frontend composition root. Generic application code imports only from
// `@/extensions`; concrete ChuanAPI wiring is evaluated exactly once by the
// application entrypoint (and explicitly by composition tests).
setNavigationExtensions([
  chuanapiNavigationExtension,
  chuanapiMonitorNavigationExtension,
])
setBrandExtension(chuanapiBrandExtension)
setHelpExtension(chuanapiHelpExtension)
setI18nExtensions([chuanapiI18nExtension])
setExtensionFlags({ showLegacyAffiliateQuota: SHOW_LEGACY_AFFILIATE_QUOTA })
setPaymentSettingsExtension(chuanapiPaymentSettingsExtension)
setWalletExtension(chuanapiWalletExtension)
setMonitorExtension(chuanapiMonitorExtension)
