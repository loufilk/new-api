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
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { handleServerError } from '@/lib/handle-server-error'
import { useAuthStore } from '@/stores/auth-store'

import { adjustUserQuota } from '../api'
import type { QuotaAdjustMode } from '../types'
import {
  beginQuotaIntent,
  finishQuotaIntent,
  quotaIntentKey,
  readQuotaIntent,
  type QuotaIntent,
  type Scope,
} from './quota-intent'

function snapshot(scope: Scope): QuotaIntent | null | 'blocked' {
  try {
    if (!navigator.locks?.request) return 'blocked'
    return readQuotaIntent(scope)
  } catch {
    return 'blocked'
  }
}

// These are stable business rejections from controller/user_quota.go with no
// committed adjustment. Unknown errors (including commit/connection errors)
// are not proof of rejection. A later permission error proves nothing about a
// previous attempt whose acknowledgment was lost.
function confirmedFirstRejection(result: {
  success: boolean
  message?: string
  message_key?: string
}) {
  return (
    result.success === false &&
    [
      'insufficient balance',
      'Wallet quota limit exceeded',
      'Invalid parameters',
      'Quota change amount cannot be zero',
      'User does not exist',
      'No permission to update users of same or higher permission level',
    ].includes(result.message_key ?? result.message ?? '')
  )
}

export function useQuotaAdjustment(scope: Scope, onSuccess: () => void) {
  const { t } = useTranslation()
  const [displayed, setDisplayed] = useState(() => snapshot(scope))
  const pending =
    displayed && displayed !== 'blocked' && displayed.status === 'pending'
      ? displayed
      : null
  const [loading, setLoading] = useState(false)
  const inFlight = useRef(false)
  const alive = useRef(true)
  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
    }
  }, [])
  const current = () =>
    alive.current && useAuthStore.getState().auth.user?.id === scope.actorId

  async function submit(mode: QuotaAdjustMode, value: number) {
    if (!current() || displayed === 'blocked' || inFlight.current) return
    inFlight.current = true
    setLoading(true)
    try {
      await navigator.locks.request(
        quotaIntentKey(scope),
        { ifAvailable: true },
        async (lock) => {
          if (!current()) return
          if (!lock) {
            setDisplayed(snapshot(scope))
            toast.info(
              t(
                'Another page is processing this quota adjustment. Reopen it before continuing.'
              )
            )
            return
          }
          const intent = beginQuotaIntent(scope, displayed, mode, value)
          if (!intent) {
            setDisplayed(snapshot(scope))
            toast.info(
              t(
                'Another page is processing this quota adjustment. Reopen it before continuing.'
              )
            )
            return
          }
          setDisplayed(intent)
          let result
          try {
            result = await adjustUserQuota({
              id: intent.userId,
              action: 'add_quota',
              mode: intent.mode,
              value: intent.value,
              request_id: intent.request_id,
            })
          } catch (error) {
            if (current()) handleServerError(error, t('Failed to adjust quota'))
            return
          }
          if (result?.success === true) {
            finishQuotaIntent(intent)
            if (current()) {
              setDisplayed(snapshot(scope))
              toast.success(t('Quota adjusted successfully'))
              onSuccess()
            }
          } else {
            if (!pending && result && confirmedFirstRejection(result)) {
              finishQuotaIntent(intent)
              if (current()) setDisplayed(snapshot(scope))
            }
            if (current()) {
              handleServerError(result, t('Failed to adjust quota'))
            }
          }
        }
      )
    } catch {
      // A blocked write prevents dispatch. A blocked completion leaves the
      // original tuple recoverable; never mint another UUID for that result.
      if (current()) setDisplayed('blocked')
    } finally {
      inFlight.current = false
      if (current()) setLoading(false)
    }
  }
  return { blocked: displayed === 'blocked', pending, loading, submit }
}
