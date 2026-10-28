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
import { createElement } from 'react'

import type { BrandExtension } from '../brand'
import type { HelpExtension } from '../help'
import type { I18nExtension } from '../i18n'
import type { MonitorExtension } from '../monitor'
import type { PaymentSettingsExtension } from '../payment-settings'
import type { WalletExtension } from '../wallet'
import { CHUANAPI_BRAND_FAVICON, CHUANAPI_BRAND_LOGO } from './brand'
import { loadChuanapiI18nOverride } from './i18n'
import {
  LazyHelpApiReference,
  LazyHelpGuide,
  LazyHelpIndex,
  LazyPaymentSettingsRenderer,
  LazyWallet,
  LazyMonitor,
  LazyMonitorSettings,
} from './lazy-pages'

export {
  CHUANAPI_PUBLIC_HOME,
  chuanapiNavigationExtension,
  chuanapiMonitorNavigationExtension,
} from './navigation'
export { SHOW_LEGACY_AFFILIATE_QUOTA } from './flags'
export { CHUANAPI_BRAND_LOGO } from './brand'
export { loadChuanapiI18nOverride } from './i18n'

export const chuanapiBrandExtension: BrandExtension = {
  id: 'chuanapi/brand',
  logoUrl: CHUANAPI_BRAND_LOGO,
  logoClassName: 'dark:invert',
  faviconUrl: CHUANAPI_BRAND_FAVICON,
}

export const chuanapiHelpExtension: HelpExtension = {
  id: 'chuanapi/help',
  indexPage: LazyHelpIndex,
  guidePage: LazyHelpGuide,
  apiReferencePage: LazyHelpApiReference,
}

export const chuanapiI18nExtension: I18nExtension = {
  id: 'chuanapi/i18n',
  loadOverride: loadChuanapiI18nOverride,
}

export const chuanapiWalletExtension: WalletExtension = {
  id: 'chuanapi/wallet',
  page: LazyWallet,
}

export const chuanapiMonitorExtension: MonitorExtension = {
  id: 'chuanapi/monitor',
  page: LazyMonitor,
  settingsPage: LazyMonitorSettings,
}

export const chuanapiPaymentSettingsExtension: PaymentSettingsExtension = {
  id: 'chuanapi/payment-settings',
  render: (settings) =>
    createElement(LazyPaymentSettingsRenderer, { settings }),
}
