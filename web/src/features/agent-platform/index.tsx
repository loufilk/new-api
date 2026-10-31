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
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useAuthStore } from '@/stores/auth-store'

import { getPlatform } from './api'
import { Commissions } from './components/commissions'
import { Deliveries } from './components/deliveries'
import { FundLedgerTable } from './components/fund-accounting'
import { FundBatches } from './components/fund-batches'
import { PendingOperations } from './components/pending-operations'
import { PolicyPanel } from './components/policy-panel'
import { PurchaseRefunds } from './components/purchase-refunds'
import { Purchases } from './components/purchases'
import { BusinessState } from './components/values'
import { Withdrawals } from './components/withdrawals'
import { portalSections, type PortalSection } from './sections'
import type { AgentPolicy, PlatformSelf } from './types'

export function AgentPortal(props: {
  section: PortalSection
  onSectionChange: (section: PortalSection) => void
}) {
  const { t } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const self = useQuery({
    queryKey: ['agent-platform', userId, 'self'],
    queryFn: ({ signal }) =>
      getPlatform<PlatformSelf>('/self', undefined, signal),
  })
  const policy = useQuery({
    queryKey: ['agent-platform', userId, 'policy'],
    queryFn: ({ signal }) =>
      getPlatform<AgentPolicy>('/policy/current', undefined, signal),
  })
  if (self.isPending || policy.isPending) return <LoadingState />
  if (self.isError || policy.isError || !self.data || !policy.data) {
    return (
      <ErrorState
        onRetry={() => {
          void self.refetch()
          void policy.refetch()
        }}
      />
    )
  }
  const account = self.data
  const current = policy.data
  const labels: Record<PortalSection, string> = {
    overview: t('Overview'),
    purchases: t('Procurement'),
    batches: t('Funding batches'),
    ledger: t('Procurement ledger'),
    deliveries: t('Customer deliveries'),
    refunds: t('Purchase refunds'),
    commissions: t('Commissions'),
    withdrawals: t('Withdrawals'),
    policy: t('Current policy'),
  }
  const sections = portalSections.filter(
    (section) =>
      account.capabilities.history ||
      ['overview', 'commissions', 'withdrawals', 'policy'].includes(section)
  )
  const selected = sections.includes(props.section) ? props.section : 'overview'
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Agent services & commissions')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='flex flex-col gap-5'>
          <PendingOperations key={userId} />
          <p className='text-muted-foreground text-sm'>
            {t(
              'One ordinary user account, with separate business eligibility and separate financial ledgers.'
            )}
          </p>
          <Tabs
            value={selected}
            onValueChange={(value) => {
              if (sections.includes(value as PortalSection)) {
                props.onSectionChange(value as PortalSection)
              }
            }}
          >
            <TabsList
              className='h-auto max-w-full flex-wrap'
              aria-label={t('Agent portal sections')}
            >
              {sections.map((section) => (
                <TabsTrigger key={section} value={section}>
                  {labels[section]}
                </TabsTrigger>
              ))}
            </TabsList>
            <TabsContent value='overview'>
              <div className='grid grid-cols-1 gap-4 lg:grid-cols-2'>
                <Card>
                  <CardHeader>
                    <CardTitle>{t('Business eligibility')}</CardTitle>
                    <CardDescription>
                      {t(
                        'Business freezes never mean your ordinary user account is banned.'
                      )}
                    </CardDescription>
                  </CardHeader>
                  <CardContent>
                    {account.profile ? (
                      <dl className='grid grid-cols-2 gap-3 text-sm'>
                        <div>
                          <dt>{t('Eligibility')}</dt>
                          <dd>
                            <BusinessState value={account.profile.status} />
                          </dd>
                        </div>
                        <div>
                          <dt>{t('Permission version')}</dt>
                          <dd>{account.profile.version}</dd>
                        </div>
                        <div>
                          <dt>{t('Sales freeze')}</dt>
                          <dd>
                            {account.profile.sales_frozen
                              ? t('Frozen')
                              : t('Not frozen')}
                          </dd>
                        </div>
                        <div>
                          <dt>{t('Settlement freeze')}</dt>
                          <dd>
                            {account.profile.settlement_frozen
                              ? t('Frozen')
                              : t('Not frozen')}
                          </dd>
                        </div>
                      </dl>
                    ) : (
                      <p>
                        {t(
                          'No agent eligibility is enabled. Ordinary invitation commissions, when configured, are still available to eligible users.'
                        )}
                      </p>
                    )}
                  </CardContent>
                </Card>
                <Card>
                  <CardHeader>
                    <CardTitle>
                      {t('Your ordinary account remains available')}
                    </CardTitle>
                    <CardDescription>
                      {t(
                        'Procurement quota is not your consumption wallet. Self-use requires an explicit transfer; old disabled API keys are not restored.'
                      )}
                    </CardDescription>
                  </CardHeader>
                  <CardContent>
                    <nav
                      className='flex flex-wrap gap-4'
                      aria-label={t('Ordinary user services')}
                    >
                      <Link
                        className='underline underline-offset-4'
                        to='/wallet'
                      >
                        {t('Wallet')}
                      </Link>
                      <Link className='underline underline-offset-4' to='/keys'>
                        {t('API Keys')}
                      </Link>
                      <Link
                        className='underline underline-offset-4'
                        to='/playground'
                      >
                        {t('Text Generation')}
                      </Link>
                      <Link
                        className='underline underline-offset-4'
                        to='/security'
                      >
                        {t('Security & Access')}
                      </Link>
                      <Link
                        className='underline underline-offset-4'
                        to='/agent-platform/claim'
                      >
                        {t('Claim a delivery')}
                      </Link>
                    </nav>
                  </CardContent>
                </Card>
                <Alert className='lg:col-span-2'>
                  <AlertDescription>
                    {t(
                      'Customer claims are protected independently of agent status. Existing free budgets and historical rewards are not automatically converted into refundable or withdrawable cash.'
                    )}
                  </AlertDescription>
                </Alert>
              </div>
            </TabsContent>
            {account.capabilities.history && (
              <>
                <TabsContent value='purchases'>
                  <Purchases
                    salesEnabled={account.capabilities.sales}
                    purchaseEnabled={current.purchase_enabled}
                  />
                </TabsContent>
                <TabsContent value='batches'>
                  <FundBatches
                    settlementEnabled={account.capabilities.settlement}
                    salesEnabled={account.capabilities.sales}
                    refundEnabled={current.purchase_refund_enabled}
                    selfUseEnabled={current.procurement_self_use_enabled}
                  />
                </TabsContent>
                <TabsContent value='ledger'>
                  <FundLedgerTable />
                </TabsContent>
                <TabsContent value='deliveries'>
                  <Deliveries
                    salesEnabled={account.capabilities.sales}
                    deliveryEnabled={current.delivery_enabled}
                  />
                </TabsContent>
                <TabsContent value='refunds'>
                  <PurchaseRefunds />
                </TabsContent>
              </>
            )}
            <TabsContent value='commissions'>
              <Commissions />
            </TabsContent>
            <TabsContent value='withdrawals'>
              <Withdrawals />
            </TabsContent>
            <TabsContent value='policy'>
              <PolicyPanel />
            </TabsContent>
          </Tabs>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
