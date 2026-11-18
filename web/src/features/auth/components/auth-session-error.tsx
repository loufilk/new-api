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
import { useRouter, type ErrorComponentProps } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { GeneralError } from '@/features/errors/general-error'
import { toIntlLocale } from '@/i18n/languages'
import { AuthSessionUnavailableError } from '@/lib/auth-session'
import { formatNumber } from '@/lib/format'

export function AuthSessionError(props: ErrorComponentProps) {
  if (!(props.error instanceof AuthSessionUnavailableError)) {
    return <GeneralError error={props.error} />
  }
  return <AuthSessionRecovery key={props.error.retryAt} error={props.error} />
}

function AuthSessionRecovery(props: { error: AuthSessionUnavailableError }) {
  const { t, i18n } = useTranslation()
  const router = useRouter()
  const [now, setNow] = useState(Date.now)
  const [retrying, setRetrying] = useState(false)
  const remaining = Math.max(0, Math.ceil((props.error.retryAt - now) / 1000))
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)

  useEffect(() => {
    // Recompute against the absolute deadline so suspended/background tabs
    // do not extend the server's cooldown by counting delayed timer ticks.
    // Always tick once: the deadline may pass between render and this effect.
    const timer = window.setInterval(() => {
      const current = Date.now()
      setNow(current)
      if (current >= props.error.retryAt) window.clearInterval(timer)
    }, 1000)
    return () => window.clearInterval(timer)
  }, [props.error.retryAt])

  return (
    <main className='bg-background flex min-h-svh items-center justify-center px-4 py-10'>
      <ErrorState
        title={t('Session verification is temporarily unavailable')}
        description={t('Please wait, then retry to continue to this page.')}
        className='w-full max-w-lg'
        action={
          <Button
            variant='outline'
            disabled={remaining > 0 || retrying}
            aria-busy={retrying}
            onClick={async () => {
              setRetrying(true)
              try {
                await router.invalidate()
              } finally {
                setRetrying(false)
              }
            }}
          >
            {remaining > 0
              ? t('Retry in {{seconds}}s', {
                  seconds: formatNumber(remaining, locale),
                })
              : t('Retry')}
          </Button>
        }
      />
    </main>
  )
}
