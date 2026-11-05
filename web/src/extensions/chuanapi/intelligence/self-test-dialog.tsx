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
import { Link } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'

import { eligibleTestTokens } from './token-selection'
import type { TestSelection, TestToken } from './types'

export function SelfTestDialog(props: {
  model: string
  group: string
  tokens: TestToken[]
  maxOutputTokens: number
  pending: boolean
  disabled: boolean
  onClose: () => void
  onEstimate: (selection: TestSelection) => void
}) {
  const { t } = useTranslation()
  const [tokenID, setTokenID] = useState('')
  const eligible = eligibleTestTokens(props.tokens, props.group, props.model)
  const selected = eligible.find((token) => String(token.id) === tokenID)
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={t('fingerprint.selfTest')}
      description={t('fingerprint.privateBilling')}
      footer={
        <>
          <Button variant='outline' onClick={props.onClose}>
            {t('Cancel')}
          </Button>
          <Button
            disabled={props.pending || props.disabled || !selected}
            onClick={() => {
              if (selected) {
                props.onEstimate({
                  model: props.model,
                  group: props.group,
                  token_id: selected.id,
                  max_output_tokens: props.maxOutputTokens,
                })
              }
            }}
          >
            {props.pending
              ? t('intelligence.estimating')
              : t('intelligence.reviewBudget')}
          </Button>
        </>
      }
    >
      <div className='flex flex-col gap-4'>
        <p className='font-medium break-all'>
          {props.group} · {props.model}
        </p>
        {eligible.length ? (
          <div className='flex flex-col gap-2'>
            <Label htmlFor='fingerprint-token'>
              {t('fingerprint.selectKey')}
            </Label>
            <NativeSelect
              id='fingerprint-token'
              className='w-full'
              value={tokenID}
              disabled={props.pending}
              onChange={(event) => setTokenID(event.target.value)}
            >
              <NativeSelectOption value=''>
                {t('fingerprint.chooseKey')}
              </NativeSelectOption>
              {eligible.map((token) => (
                <NativeSelectOption key={token.id} value={token.id}>
                  {token.name || `#${token.id}`} · {token.group}
                </NativeSelectOption>
              ))}
            </NativeSelect>
            <p className='text-muted-foreground text-xs'>
              {t('fingerprint.keysSafe')}
            </p>
          </div>
        ) : (
          <>
            <EmptyState
              className='min-h-24'
              title={t('fingerprint.noKey')}
              description={t('fingerprint.noKeyHelp')}
            />
            <Button variant='outline' render={<Link to='/keys' />}>
              {t('fingerprint.createKey')}
            </Button>
          </>
        )}
      </div>
    </Dialog>
  )
}
