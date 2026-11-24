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
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { API_BASE_URL } from '@/lib/api-url'
import { getAuthSessionEpoch } from '@/lib/auth-session'

import {
  describeMagpieProvider,
  isMagpieEligibleKey,
  MAGPIE_HELP_URL,
  magpieImportUrl,
  supportsMagpieDesktop,
} from '../../lib/magpie-import'
import type { ApiKey } from '../../types'
import { useApiKeys } from '../api-keys-provider'

const PREPARED_LIFETIME_MS = 60_000

type ImportStep =
  | 'review'
  | 'preparing'
  | 'ready'
  | 'failed'
  | 'expired'
  | 'requested'

type PendingImport = {
  abort: AbortController
  epoch: number
  key?: string
  deadline?: number
  timer?: ReturnType<typeof setTimeout>
}

type Props = {
  token: ApiKey
  onClose: () => void
}

export function MagpieImportDialog(props: Props) {
  const { t } = useTranslation()
  const { resolveTransientKey } = useApiKeys()
  const [step, setStep] = useState<ImportStep>('review')
  const pending = useRef<PendingImport | null>(null)
  const provider = describeMagpieProvider(
    API_BASE_URL || window.location.origin,
    props.token.id
  )
  const desktop = supportsMagpieDesktop(navigator)
  const eligible = isMagpieEligibleKey(props.token)
  const canPrepare = desktop && eligible && provider !== null

  const discard = useCallback(() => {
    const active = pending.current
    pending.current = null
    if (!active) return
    active.key = undefined
    clearTimeout(active.timer)
    active.abort.abort()
  }, [])

  useEffect(() => discard, [discard])

  async function prepare() {
    if (!canPrepare || !isMagpieEligibleKey(props.token) || pending.current) {
      return
    }
    const active: PendingImport = {
      abort: new AbortController(),
      epoch: getAuthSessionEpoch(),
    }
    pending.current = active
    setStep('preparing')
    const key = await resolveTransientKey(props.token.id, active.abort.signal)
    if (
      pending.current !== active ||
      active.abort.signal.aborted ||
      getAuthSessionEpoch() !== active.epoch
    ) {
      return
    }
    if (!key || !isMagpieEligibleKey(props.token)) {
      discard()
      setStep('failed')
      return
    }
    active.key = key
    active.deadline = performance.now() + PREPARED_LIFETIME_MS
    active.timer = setTimeout(() => {
      if (pending.current !== active) return
      discard()
      setStep('expired')
    }, PREPARED_LIFETIME_MS)
    setStep('ready')
  }

  function launch() {
    const active = pending.current
    if (
      !provider ||
      !canPrepare ||
      !isMagpieEligibleKey(props.token) ||
      !active?.key ||
      active.deadline === undefined ||
      performance.now() >= active.deadline ||
      getAuthSessionEpoch() !== active.epoch
    ) {
      discard()
      setStep('expired')
      return
    }
    const href = magpieImportUrl(provider, active.key)
    discard()
    if (!href) {
      setStep('failed')
      return
    }
    try {
      // This runs synchronously in the user's second click. noopener can
      // return null even on success; the browser cannot attest installation.
      window.open(href, '_blank', 'noopener,noreferrer')
      setStep('requested')
    } catch {
      setStep('failed')
    }
  }

  function close() {
    discard()
    props.onClose()
  }

  return (
    <Dialog
      open
      onOpenChange={(open) => !open && close()}
      title={t('Add to Magpie')}
      description={t('Import this API Key into the Magpie desktop app.')}
      contentClassName='sm:max-w-md'
      bodyClassName='space-y-3'
      footer={
        <>
          <Button variant='outline' onClick={close}>
            {t('Cancel')}
          </Button>
          {canPrepare &&
            (step === 'ready' ? (
              <Button onClick={launch}>{t('Open Magpie')}</Button>
            ) : (
              <Button
                onClick={() => void prepare()}
                disabled={step === 'preparing'}
              >
                {step === 'preparing' ? t('Loading...') : t('Prepare import')}
              </Button>
            ))}
        </>
      }
    >
      {provider && (
        <div className='space-y-1'>
          <p className='text-sm font-medium'>{t('Destination API')}</p>
          <code className='block text-xs break-all'>{provider.anthropic}</code>
        </div>
      )}
      {canPrepare && (
        <p className='text-muted-foreground text-sm'>
          {t(
            'Your browser will pass this key to the desktop app registered for Magpie. Confirm adding the provider in Magpie.'
          )}
        </p>
      )}
      {!desktop && (
        <p className='text-sm'>
          {t(
            'Open this page in a desktop browser with Magpie installed to import a key.'
          )}
        </p>
      )}
      {desktop && !provider && (
        <p role='alert' className='text-destructive text-sm'>
          {t(
            'The configured API address is not suitable for a secure Magpie import.'
          )}
        </p>
      )}
      {desktop && provider && !eligible && (
        <p role='alert' className='text-destructive text-sm'>
          {t('Use an enabled, unexpired API Key to import into Magpie.')}
        </p>
      )}
      {step === 'failed' && (
        <p role='alert' className='text-destructive text-sm'>
          {t(
            'Could not prepare or open the Magpie import. Try again or open Magpie help.'
          )}
        </p>
      )}
      {step === 'expired' && (
        <p role='status' className='text-sm'>
          {t('The import expired. Prepare it again to continue.')}
        </p>
      )}
      {step === 'requested' && (
        <p role='status' className='text-sm'>
          {t(
            'Check your browser and Magpie for a confirmation. If nothing opened, install Magpie or open Magpie help.'
          )}
        </p>
      )}
      <a
        className='text-primary text-sm underline underline-offset-4'
        href={MAGPIE_HELP_URL}
        rel='noopener noreferrer'
        target='_blank'
        onClick={() => {
          discard()
          if (step === 'preparing' || step === 'ready') setStep('review')
        }}
      >
        {t('Magpie help')}
      </a>
    </Dialog>
  )
}
