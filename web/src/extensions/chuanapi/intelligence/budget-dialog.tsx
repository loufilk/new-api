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
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { formatQuota } from '@/lib/format'

import type { TestEstimate } from './types'

export function BudgetDialog(props: {
  quote: TestEstimate
  onClose: () => void
  onConfirm: () => void
}) {
  const { t } = useTranslation()
  const [expired, setExpired] = useState(
    props.quote.expires_at <= Date.now() / 1000
  )
  useEffect(() => {
    const timer = setTimeout(
      () => setExpired(true),
      Math.max(0, props.quote.expires_at * 1000 - Date.now())
    )
    return () => clearTimeout(timer)
  }, [props.quote.expires_at])
  return (
    <ConfirmDialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={t('intelligence.confirmTitle')}
      desc={t('intelligence.confirmDescription')}
      confirmText={
        <span className='whitespace-normal'>
          {t('intelligence.confirmStart')}
        </span>
      }
      className='[&_button]:h-auto [&_button]:min-h-8 [&_button]:py-2'
      handleConfirm={props.onConfirm}
      disabled={expired}
    >
      <div className='flex flex-col gap-3 text-sm'>
        <p className='break-all'>
          {props.quote.group} / {props.quote.model}
        </p>
        <p>
          {t('fingerprint.selectKey')}: #{props.quote.token_id}
        </p>
        <p>
          {props.quote.output_unbounded || props.quote.max_output_tokens === 0
            ? t('fingerprint.unlimitedOutput', {
                seconds: props.quote.timeout_seconds,
              })
            : t('intelligence.budget', {
                tokens: props.quote.max_output_tokens,
                seconds: props.quote.timeout_seconds,
              })}
        </p>
        <p>
          {t('intelligence.estimate')}:{' '}
          {props.quote.estimated_quota === null
            ? t('intelligence.estimateUnavailable')
            : formatQuota(props.quote.estimated_quota)}
        </p>
        <Alert>
          <AlertDescription>{t('intelligence.notHardCap')}</AlertDescription>
        </Alert>
        <p className='text-muted-foreground'>
          {t('intelligence.cancelBilling')}
        </p>
        {expired && (
          <p role='alert' className='text-destructive'>
            {t('intelligence.expired')}
          </p>
        )}
      </div>
    </ConfirmDialog>
  )
}
