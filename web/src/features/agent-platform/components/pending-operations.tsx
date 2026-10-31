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
import { useQueryClient } from '@tanstack/react-query'
import { useEffect, useReducer, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { handleServerError } from '@/lib/handle-server-error'
import { useAuthStore } from '@/stores/auth-store'

import { recoverOperation } from '../api'
import { isIssuedDeliveryLink } from '../lib/delivery-link'
import {
  forgetOperation,
  pendingOperations,
  type PendingOperation,
} from '../lib/recovery'
import {
  DeliveryClaimLink,
  type IssuedDeliveryLink,
} from './delivery-claim-link'

export function PendingOperations() {
  const { t } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const [, refresh] = useReducer((revision: number) => revision + 1, 0)
  const pending = userId ? pendingOperations(userId) : []
  const [recoveredDelivery, setRecoveredDelivery] = useState<{
    owner: number
    value: IssuedDeliveryLink
  } | null>(null)
  const [checking, setChecking] = useState<string | null>(null)
  const [unresolved, setUnresolved] = useState<string | null>(null)
  const client = useQueryClient()
  useEffect(() => {
    const update = () => refresh()
    window.addEventListener('agent-platform-pending-changed', update)
    return () =>
      window.removeEventListener('agent-platform-pending-changed', update)
  }, [userId])
  const check = async (operation: PendingOperation) => {
    if (!userId || checking) return
    setChecking(operation.request_id)
    try {
      const page = await recoverOperation(operation)
      if (page.status === 'not_observed' || page.status === 'pending') {
        setUnresolved(operation.request_id)
        return
      }
      if (
        operation.action === 'delivery.create' &&
        page.action === 'delivery.create' &&
        page.resource_state === 'created' &&
        isIssuedDeliveryLink(page.resource)
      ) {
        setRecoveredDelivery({ owner: userId, value: page.resource })
      }
      forgetOperation(userId, operation.request_id)
      refresh()
      toast.info(t('Operation recorded'), {
        description: [page.resource_type, page.resource_id, page.resource_state]
          .filter(Boolean)
          .join(' · '),
      })
      void client.invalidateQueries({ queryKey: ['agent-platform'] })
    } catch (error) {
      handleServerError(error)
    } finally {
      setChecking(null)
    }
  }
  return (
    <>
      {pending.length > 0 && (
        <Alert variant='destructive'>
          <AlertTitle>
            {t('Unresolved operations from this browser')}
          </AlertTitle>
          <AlertDescription className='flex flex-col gap-3'>
            <p>
              {t(
                'Check these business requests before submitting replacements. No payment or authorization is retried automatically.'
              )}
            </p>
            {pending.map((operation) => (
              <div
                key={operation.request_id}
                className='flex flex-wrap items-center gap-2'
              >
                <span className='font-mono break-all'>
                  {operation.request_id}
                </span>
                <CopyButton
                  value={operation.request_id}
                  aria-label={t('Copy request ID')}
                />
                <Button
                  size='sm'
                  variant='outline'
                  disabled={checking !== null}
                  onClick={() => {
                    void check(operation)
                  }}
                >
                  {t('Check request status')}
                </Button>
                {unresolved === operation.request_id && (
                  <span>
                    {t(
                      'No record found yet. Contact finance with this request ID.'
                    )}
                  </span>
                )}
              </div>
            ))}
          </AlertDescription>
        </Alert>
      )}
      {recoveredDelivery && recoveredDelivery.owner === userId && (
        <DeliveryClaimLink
          delivery={recoveredDelivery.value}
          onClose={() => setRecoveredDelivery(null)}
        />
      )}
    </>
  )
}
