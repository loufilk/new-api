import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { getAgentAccounts } from './api'
import { AccountsTable } from './components/accounts-table'
import { AllocationDialog } from './components/allocation-dialog'
import { InvitationPolicySettings } from './components/invitation-policy'
import { InvitationsTable } from './components/invitations-table'
import { LedgerTable } from './components/ledger-table'
import { QuotaSummary } from './components/quota-summary'
import type { AgentAccount } from './types'

export function AgentRedemption() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const isAgent = user?.role === ROLE.AGENT
  const [pagination, setPagination] = useState({ pageIndex: 0, pageSize: 20 })
  const [filter, setFilter] = useState('')
  const [agentId, setAgentId] = useState<number | undefined>()
  const [selected, setSelected] = useState<AgentAccount | null>(null)
  const [adjusting, setAdjusting] = useState<AgentAccount | null>(null)
  const scopedAgentId = isAgent ? user.id : agentId
  const query = useQuery({
    queryKey: ['agent-redemption', user?.id, user?.role, 'accounts', scopedAgentId, pagination],
    queryFn: ({ signal }) => getAgentAccounts({ p: pagination.pageIndex + 1, page_size: pagination.pageSize, agent_user_id: scopedAgentId }, signal),
  })
  const account = query.data?.items.find((item) => item.agent_user_id === user?.id)
  const selectedAccount = query.data?.items.find((item) => item.agent_user_id === selected?.agent_user_id) ?? selected

  return <SectionPageLayout>
    <SectionPageLayout.Title>{isAgent ? t('Quota & Invitations') : t('Agent Management')}</SectionPageLayout.Title>
    <SectionPageLayout.Content>
      <div className='space-y-5'>
        <p className='text-muted-foreground text-sm'>{t('Creating codes reserves quota. Redemption spends it. Disabling, deleting, or expiring unused codes releases the reservation.')}</p>
        {!isAgent && <form className='flex flex-wrap items-end gap-2' onSubmit={(event) => { event.preventDefault(); setAgentId(filter ? Number(filter) : undefined); setPagination((previous) => ({ ...previous, pageIndex: 0 })) }}>
          <div className='space-y-1'><Label htmlFor='agent-account-filter'>{t('Agent ID')}</Label><Input id='agent-account-filter' type='number' min={1} step={1} value={filter} onChange={(event) => setFilter(event.target.value)} placeholder={t('All agents')} /></div>
          <Button type='submit' variant='outline'>{t('Filter')}</Button>
          {agentId !== undefined && <Button type='button' variant='ghost' onClick={() => { setFilter(''); setAgentId(undefined); setPagination((previous) => ({ ...previous, pageIndex: 0 })) }}>{t('Clear')}</Button>}
        </form>}
        {query.isError && <ErrorState onRetry={() => { void query.refetch() }} />}
        {isAgent && query.isLoading && <LoadingState />}
        {isAgent && account && <QuotaSummary account={account} />}
        <Tabs defaultValue={isAgent ? 'ledger' : 'accounts'}>
          <TabsList className='h-auto max-w-full flex-wrap' aria-label={t('Agent management sections')}>
            {!isAgent && <TabsTrigger value='accounts'>{t('Agent accounts')}</TabsTrigger>}
            {isAgent && <TabsTrigger value='ledger'>{t('Quota ledger')}</TabsTrigger>}
            <TabsTrigger value='invitations'>{t('Invitations')}</TabsTrigger>
            {user?.role === ROLE.SUPER_ADMIN && <TabsTrigger value='policy'>{t('Invitation policy')}</TabsTrigger>}
          </TabsList>
          {!isAgent && <TabsContent value='accounts'>{!query.isError && <AccountsTable data={query.data} isLoading={query.isLoading} isFetching={query.isFetching} pagination={pagination} onPaginationChange={setPagination} onSelect={setSelected} onAdjust={setAdjusting} />}</TabsContent>}
          {isAgent && user && <TabsContent value='ledger'><LedgerTable agentId={user.id} /></TabsContent>}
          <TabsContent value='invitations'><InvitationsTable key={scopedAgentId ?? 'all'} agentId={scopedAgentId} /></TabsContent>
          {user?.role === ROLE.SUPER_ADMIN && <TabsContent value='policy'><InvitationPolicySettings /></TabsContent>}
        </Tabs>
      </div>
      {!isAgent && adjusting && <AllocationDialog account={adjusting} onClose={() => setAdjusting(null)} />}
      {!isAgent && selectedAccount && <Dialog open onOpenChange={(open) => { if (!open) setSelected(null) }} title={t('Quota ledger')} description={`${t('Agent')} #${selectedAccount.agent_user_id}`} contentClassName='sm:max-w-6xl'>
        <div className='space-y-4'><QuotaSummary account={selectedAccount} /><LedgerTable key={selectedAccount.agent_user_id} agentId={selectedAccount.agent_user_id} /></div>
      </Dialog>}
    </SectionPageLayout.Content>
  </SectionPageLayout>
}
