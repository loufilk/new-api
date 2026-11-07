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
import { lazy, Suspense, type ReactNode } from 'react'

import type {
  HelpApiReferencePage,
  HelpGuidePage,
  HelpIndexPage,
} from '../help'
import type { WalletBenefitBannerProps, WalletPage } from '../wallet'

const LazyDocsIndexPage = lazy(() =>
  import('./help/pages/docs-index').then(({ DocsIndexPage }) => ({
    default: DocsIndexPage,
  }))
)
const LazyGuidePage = lazy(() =>
  import('./help/pages/guide-page').then(({ GuidePage }) => ({
    default: GuidePage,
  }))
)
const LazyApiReferencePage = lazy(() =>
  import('./help/pages/api-reference-page').then(({ ApiReferencePage }) => ({
    default: ApiReferencePage,
  }))
)
const LazyWalletPage = lazy(() =>
  import('./wallet').then(({ Wallet }) => ({ default: Wallet }))
)
const LazyBenefitBanner = lazy(() =>
  import('./new-user-benefit/benefit-banner').then(
    ({ NewUserBenefitBanner }) => ({
      default: NewUserBenefitBanner,
    })
  )
)
const LazyBenefitSettings = lazy(() =>
  import('./new-user-benefit/benefit-settings').then(
    ({ NewUserBenefitSettings }) => ({
      default: NewUserBenefitSettings,
    })
  )
)
const LazyReferralPanel = lazy(() =>
  import('./wallet/components/referral-panel').then(({ ReferralPanel }) => ({
    default: ReferralPanel,
  }))
)
const LazyPaymentSettingsPage = lazy(() =>
  import('./payment').then(({ chuanapiPaymentSettingsExtension }) => ({
    default: ({ settings }: { settings: unknown }) => (
      <>{chuanapiPaymentSettingsExtension.render(settings)}</>
    ),
  }))
)
const LazyAccountPoolPage = lazy(() =>
  import('./account-pool').then(({ AccountPoolPage }) => ({
    default: AccountPoolPage,
  }))
)
const LazyPoolDiagnostics = lazy(() =>
  import('./account-pool/diagnostics').then(({ PoolDiagnostics }) => ({
    default: PoolDiagnostics,
  }))
)
const LazyAffiliatePage = lazy(() =>
  import('./affiliate').then(({ AffiliatePage }) => ({
    default: AffiliatePage,
  }))
)
const LazyIntelligencePage = lazy(() =>
  import('./intelligence').then(({ IntelligencePage }) => ({
    default: IntelligencePage,
  }))
)

function ExtensionLoading() {
  return <div className='min-h-24' role='status' aria-label='Loading' />
}

function withLoading(content: ReactNode) {
  return <Suspense fallback={<ExtensionLoading />}>{content}</Suspense>
}

export const LazyHelpIndex: HelpIndexPage = () =>
  withLoading(<LazyDocsIndexPage />)

export const LazyHelpGuide: HelpGuidePage = ({ slug }) =>
  withLoading(<LazyGuidePage slug={slug} />)

export const LazyHelpApiReference: HelpApiReferencePage = (props) =>
  withLoading(<LazyApiReferencePage {...props} />)

export const LazyWallet: WalletPage = (props) =>
  withLoading(<LazyWalletPage {...props} />)

export const LazyWalletReferralPanel = () => withLoading(<LazyReferralPanel />)

export function LazyWalletBenefitBanner(props: WalletBenefitBannerProps) {
  return (
    <Suspense fallback={null}>
      <LazyBenefitBanner {...props} />
    </Suspense>
  )
}

export const LazyNewUserBenefitSettings = () =>
  withLoading(<LazyBenefitSettings />)

export const LazyAccountPool = () => withLoading(<LazyAccountPoolPage />)

export const LazyAccountPoolDiagnostics = () =>
  withLoading(<LazyPoolDiagnostics />)

export const LazyAffiliate = () => withLoading(<LazyAffiliatePage />)

export const LazyIntelligence = () => withLoading(<LazyIntelligencePage />)

export function LazyPaymentSettingsRenderer({
  settings,
}: {
  settings: unknown
}) {
  return withLoading(<LazyPaymentSettingsPage settings={settings} />)
}
