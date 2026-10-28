import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'

import { useAgentAccount } from '../hooks/use-agent-account'
import { QuotaSummary } from './quota-summary'

export function AgentBudget() {
  const { t } = useTranslation()
  const query = useAgentAccount()
  if (query.isError) return <ErrorState onRetry={() => { void query.refetch() }} />
  if (query.isLoading) return <LoadingState inline message={t('Loading...')} />
  if (!query.account) return null
  return <QuotaSummary account={query.account} />
}
