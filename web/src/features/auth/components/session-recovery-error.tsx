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
import {
  useRouter,
  useRouterState,
  type ErrorComponentProps,
} from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { GeneralError } from '@/features/errors/general-error'
import { AuthRefreshUnavailable } from '@/lib/auth-refresh-backoff'

function SessionRecovery(props: { retryAt: number }) {
  const { t } = useTranslation()
  const router = useRouter()
  const loading = useRouterState({ select: (state) => state.isLoading })
  const [now, setNow] = useState(Date.now)
  const remaining = Math.max(0, Math.ceil((props.retryAt - now) / 1000))
  useEffect(() => {
    if (props.retryAt <= Date.now()) {
      setNow(Date.now())
      return
    }
    // Compare against the real deadline: a backgrounded tab may skip ticks.
    const timer = window.setInterval(() => {
      const current = Date.now()
      setNow(current)
      if (current >= props.retryAt) window.clearInterval(timer)
    }, 1000)
    return () => window.clearInterval(timer)
  }, [props.retryAt])

  return (
    <main>
      <ErrorState
        className='min-h-svh'
        title={t('Session')}
        description={t('Failed to refresh credentials. Please try again later')}
        action={
          <Button
            variant='outline'
            size='sm'
            disabled={remaining > 0 || loading}
            onClick={() => void router.invalidate()}
          >
            {remaining > 0
              ? t('auth.resetPasswordConfirm.retry', { seconds: remaining })
              : t('Retry')}
          </Button>
        }
      />
    </main>
  )
}

export function SessionRecoveryError(props: ErrorComponentProps) {
  if (props.error instanceof AuthRefreshUnavailable) {
    return <SessionRecovery retryAt={props.error.retryAt} />
  }
  return <GeneralError error={props.error} />
}
