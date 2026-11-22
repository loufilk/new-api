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

import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { InvalidPerformanceSummaryError } from '@/features/performance-metrics/lib/summary-response'
import { getServerErrorStatus } from '@/lib/server-error-message'

type PerformanceUnavailableProps = {
  error: unknown
  retrying: boolean
  onRetry: () => void
}

export function PerformanceUnavailable(props: PerformanceUnavailableProps) {
  const { t } = useTranslation()
  const status = getServerErrorStatus(props.error)
  const httpStatus =
    status !== undefined &&
    Number.isInteger(status) &&
    status >= 100 &&
    status <= 599
      ? status
      : undefined

  return (
    <div
      role='alert'
      aria-label={t('Performance health')}
      aria-busy={props.retrying}
      data-error-kind={
        props.error instanceof InvalidPerformanceSummaryError
          ? 'invalid-response'
          : 'request-error'
      }
      data-http-status={httpStatus}
    >
      <ErrorState
        title={t('Performance health')}
        description={t('Unavailable')}
        className='min-h-0 rounded-lg border p-4'
        action={
          <Button
            variant='outline'
            size='sm'
            disabled={props.retrying}
            onClick={props.onRetry}
          >
            {t('Retry')}
          </Button>
        }
      />
    </div>
  )
}
