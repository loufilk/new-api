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

import { requireServerSuccess } from '@/lib/server-error-message'

import type { BrandExtension } from '../brand'
import type { ConsoleToolsExtension } from '../console-tools'
import type { HelpExtension } from '../help'
import type { I18nExtension } from '../i18n'
import type { PaymentSettingsExtension } from '../payment-settings'
import type { WalletExtension } from '../wallet'
import { CHUANAPI_BRAND_FAVICON, CHUANAPI_BRAND_LOGO } from './brand'
import { loadChuanapiI18nOverride } from './i18n'
import {
  LazyAccountPool,
  LazyAccountPoolDiagnostics,
  LazyAffiliate,
  LazyHelpApiReference,
  LazyHelpGuide,
  LazyHelpIndex,
  LazyIntelligence,
  LazyPaymentSettingsRenderer,
  LazyWallet,
  LazyWalletReferralPanel,
} from './lazy-pages'

export {
  CHUANAPI_PUBLIC_HOME,
  chuanapiNavigationExtension,
  chuanapiToolsNavigationExtension,
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
  referralPanel: LazyWalletReferralPanel,
  loadSummary: async () => {
    const { getWalletSummary } = await import('./wallet/api')
    return requireServerSuccess(await getWalletSummary()).data
  },
}

export const chuanapiConsoleToolsExtension: ConsoleToolsExtension = {
  id: 'chuanapi/console-tools',
  accountPoolPage: LazyAccountPool,
  accountPoolDiagnostics: LazyAccountPoolDiagnostics,
  verifyAccountPoolAccess: async (client) => {
    const { verifyAccountPoolAccess } = await import('./account-pool/access')
    return verifyAccountPoolAccess(client)
  },
  affiliatePage: LazyAffiliate,
  intelligencePage: LazyIntelligence,
}

export const chuanapiPaymentSettingsExtension: PaymentSettingsExtension = {
  id: 'chuanapi/payment-settings',
  render: (settings) =>
    createElement(LazyPaymentSettingsRenderer, { settings }),
}
