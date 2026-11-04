/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { ErrorState } from '@/components/error-state'
import { PublicLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { formatAgentQuota } from '@/features/agent-platform/lib/money'
import { api } from '@/lib/api'
import { submitPaymentForm } from '@/lib/payment-form'
import { useSystemConfigStore } from '@/stores/system-config-store'

interface PublicPayment {
  public_id: string
  name: string
  quota: number
  sale_price_fen: number
  status: string
  expires_at: number
  payment_url?: string
  payment_params?: string
  redemption_key?: string
  created_at: number
}

function getToken() {
  if (typeof window === 'undefined') return ''
  return new URLSearchParams(window.location.hash.slice(1)).get('token') ?? ''
}

async function getPayment(
  publicID: string,
  token: string
): Promise<PublicPayment> {
  const response = await api.post<{ success: boolean; data?: PublicPayment }>(
    `/public-api/agent-pay/${encodeURIComponent(publicID)}`,
    { token },
    { skipBusinessError: true } as Record<string, unknown>
  )
  if (!response.data.success || !response.data.data) {
    throw new Error('Payment code is unavailable')
  }
  return response.data.data
}

export function AgentPaymentPage(props: { publicID: string }) {
  const { t } = useTranslation()
  const quotaPerUnit = useSystemConfigStore(
    (state) => state.config.currency.quotaPerUnit
  )
  const [paying, setPaying] = useState(false)
  const token = getToken()
  const query = useQuery({
    queryKey: ['public-agent-payment', props.publicID, token],
    queryFn: () => getPayment(props.publicID, token),
    enabled: token.length >= 32,
    refetchInterval: (current) =>
      current.state.data?.status === 'pending' ? 5000 : false,
  })

  const startPayment = () => {
    if (!query.data?.payment_url || !query.data.payment_params || paying) return
    setPaying(true)
    try {
      const params = JSON.parse(query.data.payment_params) as Record<
        string,
        unknown
      >
      submitPaymentForm(query.data.payment_url, params)
    } finally {
      setPaying(false)
    }
  }

  let content
  if (!token || token.length < 32) {
    content = (
      <ErrorState
        title={t('Invalid payment link')}
        description={t('Open the complete payment link provided by the agent.')}
      />
    )
  } else if (query.isPending) {
    content = <LoadingState />
  } else if (query.isError || !query.data) {
    content = <ErrorState onRetry={() => void query.refetch()} />
  } else if (
    query.data.status === 'expired' ||
    query.data.status === 'closed' ||
    query.data.status === 'refunded'
  ) {
    content = (
      <ErrorState
        title={t('Payment link is no longer available')}
        description={t('Ask the agent to create a new payment link.')}
      />
    )
  } else if (query.data.redemption_key) {
    content = (
      <Card>
        <CardHeader>
          <CardTitle>
            {query.data.status === 'redeemed'
              ? t('Code redeemed')
              : t('Payment successful')}
          </CardTitle>
          {query.data.status !== 'redeemed' && (
            <CardDescription>
              {t(
                'Your redemption code is ready. Copy it to your account wallet.'
              )}
            </CardDescription>
          )}
        </CardHeader>
        <CardContent className='space-y-3'>
          <div className='flex items-center gap-2 rounded-lg border p-3'>
            <code className='min-w-0 flex-1 text-sm break-all'>
              {query.data.redemption_key}
            </code>
            <CopyButton
              value={query.data.redemption_key}
              aria-label={t('Copy redemption code')}
            />
          </div>
          <p className='text-muted-foreground text-xs'>
            {t('The code can be redeemed only once.')}
          </p>
        </CardContent>
      </Card>
    )
  } else {
    content = (
      <Card>
        <CardHeader>
          <CardTitle>{t('Redeemable balance')}</CardTitle>
          <CardDescription>{query.data.name}</CardDescription>
        </CardHeader>
        <CardContent className='space-y-5'>
          <div className='text-primary text-4xl font-semibold tabular-nums'>
            {formatAgentQuota(query.data.quota, quotaPerUnit)}
          </div>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Pay securely through the platform. The code is delivered after payment succeeds.'
            )}
          </p>
          <Button
            className='w-full'
            disabled={!query.data.payment_url || paying}
            onClick={startPayment}
          >
            {paying
              ? t('Opening payment...')
              : t('Pay {{amount}}', {
                  amount: `¥${(query.data.sale_price_fen / 100).toFixed(2)}`,
                })}
          </Button>
        </CardContent>
      </Card>
    )
  }

  return (
    <PublicLayout>
      <div className='mx-auto flex min-h-[70vh] w-full max-w-md items-center px-4 py-12'>
        <div className='w-full space-y-4'>
          <div className='text-center'>
            <h1 className='text-2xl font-semibold'>{t('Customer payment')}</h1>
            <p className='text-muted-foreground mt-1 text-sm'>
              {t('Powered by the platform')}
            </p>
          </div>
          {content}
        </div>
      </div>
    </PublicLayout>
  )
}
