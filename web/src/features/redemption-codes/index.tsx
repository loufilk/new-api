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
import { getRouteApi } from '@tanstack/react-router'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  formatAgentCash,
  formatAgentQuota as formatQuota,
} from '@/features/agent-platform/lib/money'
import { isAgentWorkspaceUser } from '@/lib/agent-permissions'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { SimpleCodeForm } from '../agent-platform/components/simple-code-form'
import { SimplePaymentForm } from '../agent-platform/components/simple-payment-form'
import { SimpleRecords } from '../agent-platform/components/simple-records'
import { getSimpleAgentBalance } from '../agent-platform/simple-api'
import { RedemptionsDialogs } from './components/redemptions-dialogs'
import { RedemptionsPrimaryButtons } from './components/redemptions-primary-buttons'
import { RedemptionsProvider } from './components/redemptions-provider'
import { RedemptionsTable } from './components/redemptions-table'

const route = getRouteApi('/_authenticated/redemption-codes/')

export function Redemptions() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const isAgent = isAgentWorkspaceUser(user)
  const search = route.useSearch()
  const navigate = route.useNavigate()
  const agentId = isAgent ? user.id : search.agent_user_id
  const view = search.view ?? (isAgent ? 'center' : 'records')
  const center = isAgent && view === 'center'
  const isRoot = user?.role === ROLE.SUPER_ADMIN
  const [simpleCodeOpen, setSimpleCodeOpen] = useState(false)
  const [simplePaymentOpen, setSimplePaymentOpen] = useState(false)
  const balance = useQuery({
    queryKey: ['agent-platform', user?.id, 'simple-balance'],
    queryFn: () => getSimpleAgentBalance(),
    enabled: center,
  })
  let balanceCard: ReactNode = (
    <ErrorState onRetry={() => void balance.refetch()} />
  )
  if (balance.data) {
    balanceCard = (
      <div className='bg-card rounded-lg border p-4'>
        <div className='flex flex-wrap items-center justify-between gap-3'>
          <div>
            <h2 className='font-semibold'>{t('Agent balance')}</h2>
            <dl className='text-muted-foreground mt-2 flex flex-wrap gap-x-5 gap-y-1 text-sm'>
              <div>
                <dt>{t('Available balance')}</dt>
                <dd className='text-foreground font-medium'>
                  {formatQuota(balance.data.available_quota)}
                </dd>
              </div>
              <div>
                <dt>{t('Available credit')}</dt>
                <dd className='text-foreground font-medium'>
                  {formatAgentCash(balance.data.available_credit_quota)}
                </dd>
              </div>
              <div>
                <dt>{t('Amount due')}</dt>
                <dd className='text-foreground font-medium'>
                  {formatAgentCash(balance.data.amount_due_quota)}
                </dd>
              </div>
            </dl>
          </div>
          <Button onClick={() => setSimpleCodeOpen(true)}>
            {t('Issue redemption code')}
          </Button>
        </div>
      </div>
    )
  } else if (balance.isPending) {
    balanceCard = <LoadingState inline />
  }
  return (
    <RedemptionsProvider>
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>
          {center ? t('Redemption Code Center') : t('Redemption Code Records')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          {(!isAgent || center) && (
            <RedemptionsPrimaryButtons
              agentId={agentId}
              center={center}
              onSimpleCreate={
                center ? () => setSimpleCodeOpen(true) : undefined
              }
            />
          )}
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <div className='flex h-full min-h-0 flex-col gap-3'>
            {center ? (
              <div className='mx-auto grid w-full max-w-4xl gap-4 sm:grid-cols-2'>
                <div className='sm:col-span-2'>{balanceCard}</div>
                <Card>
                  <CardHeader>
                    <CardTitle>{t('Issue directly')}</CardTitle>
                    <CardDescription>
                      {t(
                        'Use your agent balance to create a code, collect payment from your customer, and deliver the code yourself.'
                      )}
                    </CardDescription>
                  </CardHeader>
                  <CardContent className='text-muted-foreground text-sm'>
                    {t(
                      'Use your balance or available credit to create the code, then collect payment yourself.'
                    )}
                    <div className='mt-3'>
                      <Button
                        variant='outline'
                        onClick={() => setSimpleCodeOpen(true)}
                      >
                        {t('Issue directly')}
                      </Button>
                    </div>
                  </CardContent>
                </Card>
                <Card>
                  <CardHeader>
                    <CardTitle>{t('Generate customer payment code')}</CardTitle>
                    <CardDescription>
                      {t(
                        'A customer payment page will deliver the same type of redemption code after payment.'
                      )}
                    </CardDescription>
                  </CardHeader>
                  <CardContent className='text-muted-foreground text-sm'>
                    {t(
                      'The payment code does not consume agent balance. Profit is available after five days.'
                    )}
                    <div className='mt-3'>
                      <Button
                        variant='outline'
                        onClick={() => setSimplePaymentOpen(true)}
                      >
                        {t('Generate customer payment code')}
                      </Button>
                    </div>
                  </CardContent>
                </Card>
              </div>
            ) : (
              <div className='min-h-0 flex-1 space-y-4 overflow-y-auto'>
                {isRoot && (
                  <div className='rounded-lg border p-4'>
                    <div className='mb-3 max-w-xs space-y-1'>
                      <Label htmlFor='simple-code-agent-filter'>
                        {t('Agent ID')}
                      </Label>
                      <Input
                        id='simple-code-agent-filter'
                        type='number'
                        min={1}
                        step={1}
                        value={search.agent_user_id ?? ''}
                        placeholder={t('All agents')}
                        onChange={(event) => {
                          const value = Number(event.target.value)
                          void navigate({
                            search: (previous) => ({
                              ...previous,
                              page: 1,
                              agent_user_id:
                                Number.isSafeInteger(value) && value > 0
                                  ? value
                                  : undefined,
                            }),
                          })
                        }}
                      />
                    </div>
                    <SimpleRecords key={agentId} agentUserId={agentId} />
                  </div>
                )}
                {isAgent && (
                  <div className='rounded-lg border p-4'>
                    <SimpleRecords key={agentId} agentUserId={agentId} />
                  </div>
                )}
                {!isAgent && !isRoot && <RedemptionsTable />}
                {isRoot && (
                  <div className='min-h-[400px] space-y-3 rounded-lg border p-4'>
                    <h2 className='font-semibold'>
                      {t('Platform redemption codes')}
                    </h2>
                    <RedemptionsTable />
                  </div>
                )}
              </div>
            )}
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <RedemptionsDialogs />
      {simpleCodeOpen && balance.data && (
        <SimpleCodeForm
          balance={balance.data}
          onClose={() => setSimpleCodeOpen(false)}
        />
      )}
      {simplePaymentOpen && (
        <SimplePaymentForm onClose={() => setSimplePaymentOpen(false)} />
      )}
    </RedemptionsProvider>
  )
}
