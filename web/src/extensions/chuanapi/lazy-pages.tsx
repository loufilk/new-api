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
import type { WalletPage } from '../wallet'

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
const LazyMonitorPage = lazy(() =>
  import('./monitor').then(({ MonitorPage }) => ({ default: MonitorPage }))
)
const LazyMonitorSettingsPage = lazy(() =>
  import('./monitor/settings').then(({ MonitorSettings }) => ({
    default: MonitorSettings,
  }))
)
const LazyPaymentSettingsPage = lazy(() =>
  import('./payment').then(({ chuanapiPaymentSettingsExtension }) => ({
    default: ({ settings }: { settings: unknown }) => (
      <>{chuanapiPaymentSettingsExtension.render(settings)}</>
    ),
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

export const LazyMonitor = () => withLoading(<LazyMonitorPage />)
export const LazyMonitorSettings = () =>
  withLoading(<LazyMonitorSettingsPage />)

export function LazyPaymentSettingsRenderer({
  settings,
}: {
  settings: unknown
}) {
  return withLoading(<LazyPaymentSettingsPage settings={settings} />)
}
