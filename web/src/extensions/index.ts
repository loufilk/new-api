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
export {
  applyNavigationExtensions,
  extensionPublicHomeUrl,
  extensionQuickstartDocsUrl,
} from './navigation'
export type {
  NavigationExtension,
  NavigationExtensionContext,
} from './navigation'
export { extensionHelpPages } from './help'
export type { HelpExtension } from './help'
export {
  extensionBrandLogo,
  extensionBrandLogoClassName,
  extensionBrandFavicon,
} from './brand'
export type { BrandExtension } from './brand'
export { extensionFlags, isExtensionFeatureEnabled } from './flags'
export type { ExtensionFeatureFlag, ExtensionFlags } from './flags'
export { extensionI18nOverrides, loadExtensionI18nOverride } from './i18n'
export type {
  ExtensionLocaleBundle,
  ExtensionLocaleKey,
  I18nExtension,
} from './i18n'
export { extensionPaymentSettingsSection } from './payment-settings'
export type { PaymentSettingsExtension } from './payment-settings'
export { extensionWalletPage } from './wallet'
export type { WalletExtension, WalletPageProps } from './wallet'
