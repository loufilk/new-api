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
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useAuthStore } from '@/stores/auth-store'

import { AgreementPanel } from './components/agreements'
import { CampaignPanel } from './components/campaign'
import { ContributionsPanel } from './components/contributions'
import { CreditPanel } from './components/credit'
import { DepositPanel } from './components/deposits'
import {
  FundReconciliation,
  FundReclassifications,
} from './components/fund-accounting'
import { GovernancePanel } from './components/governance'
import { PendingOperations } from './components/pending-operations'
import { PolicyPanel } from './components/policy-panel'
import { Profiles } from './components/profiles'
import { PurchaseRefunds } from './components/purchase-refunds'
import { Purchases } from './components/purchases'
import { ReconciliationPanel } from './components/reconciliation'
import { StatementPanel } from './components/statements'
import { Withdrawals } from './components/withdrawals'
import { financeSections, type FinanceSection } from './sections'

export function AgentFinance(props: {
  section: FinanceSection
  onSectionChange: (section: FinanceSection) => void
}) {
  const { t } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const labels: Record<FinanceSection, string> = {
    governance: t('Qualification dossiers'),
    contributions: t('Invitation contributions'),
    credit: t('Credit and receivables'),
    deposits: t('Deposits'),
    campaigns: t('Campaigns'),
    agreements: t('Pricing agreements'),
    statements: t('Manual bank reconciliation'),
    purchases: t('Receipt verification'),
    refunds: t('Purchase refunds'),
    withdrawals: t('Commission withdrawals'),
    profiles: t('Agent eligibility'),
    sources: t('Historical purchase sources'),
    'fund-reconciliation': t('Procurement reconciliation'),
    reconciliation: t('Reconciliation'),
    policy: t('Versioned policy'),
  }
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Agent finance')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='flex flex-col gap-5'>
          <PendingOperations key={userId} />
          <Alert>
            <AlertDescription>
              {t(
                'This console records independently reviewed financial facts. It never sends an automatic payout. Preserve the original business number and verify evidence before confirming any result.'
              )}
            </AlertDescription>
          </Alert>
          <Tabs
            value={props.section}
            onValueChange={(value) => {
              if (financeSections.includes(value as FinanceSection)) {
                props.onSectionChange(value as FinanceSection)
              }
            }}
          >
            <TabsList
              className='h-auto max-w-full flex-wrap'
              aria-label={t('Finance sections')}
            >
              {financeSections.map((section) => (
                <TabsTrigger key={section} value={section}>
                  {labels[section]}
                </TabsTrigger>
              ))}
            </TabsList>
            <TabsContent value='governance'>
              <GovernancePanel finance />
            </TabsContent>
            <TabsContent value='contributions'>
              <ContributionsPanel finance />
            </TabsContent>
            <TabsContent value='credit'>
              <CreditPanel finance />
            </TabsContent>
            <TabsContent value='deposits'>
              <DepositPanel finance />
            </TabsContent>
            <TabsContent value='campaigns'>
              <CampaignPanel finance />
            </TabsContent>
            <TabsContent value='agreements'>
              <AgreementPanel />
            </TabsContent>
            <TabsContent value='statements'>
              <StatementPanel />
            </TabsContent>
            <TabsContent value='purchases'>
              <Purchases finance />
            </TabsContent>
            <TabsContent value='refunds'>
              <PurchaseRefunds finance />
            </TabsContent>
            <TabsContent value='withdrawals'>
              <Withdrawals finance />
            </TabsContent>
            <TabsContent value='profiles'>
              <Profiles />
            </TabsContent>
            <TabsContent value='sources'>
              <FundReclassifications />
            </TabsContent>
            <TabsContent value='fund-reconciliation'>
              <FundReconciliation />
            </TabsContent>
            <TabsContent value='reconciliation'>
              <ReconciliationPanel />
            </TabsContent>
            <TabsContent value='policy'>
              <PolicyPanel finance />
            </TabsContent>
          </Tabs>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
