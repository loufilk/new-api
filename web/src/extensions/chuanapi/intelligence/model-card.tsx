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
import { Cpu, Play } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { getLobeIcon } from '@/lib/lobe-icon'

import { ResultRow } from './result-row'
import type { TestRun } from './types'

export function ModelTestCard(props: {
  model: string
  group: string
  runs: TestRun[]
  siteRuns?: TestRun[]
  disabled: boolean
  estimating: boolean
  allowTest?: boolean
  onEstimate: () => void
  onSelect: (run: TestRun) => void
}) {
  const { t } = useTranslation()
  // This is the configured model name's brand, never an identity verdict.
  const isOpenAIName = /^(gpt-|o[134](?:-|$))/i.test(props.model)
  return (
    <article className='border-border/60 grid min-w-0 border-t md:grid-cols-[9rem_minmax(0,1fr)]'>
      <div className='border-border/60 flex min-w-0 items-center gap-3 border-b px-4 py-3 md:flex-col md:items-start md:justify-center md:gap-2.5 md:border-r md:border-b-0'>
        <span
          aria-hidden
          className='flex size-7 shrink-0 items-center justify-center'
        >
          {isOpenAIName ? (
            getLobeIcon('OpenAI', 23)
          ) : (
            <Cpu className='size-5' />
          )}
        </span>
        <h3 className='min-w-0 text-sm font-medium break-all'>{props.model}</h3>
      </div>
      <div className='divide-border/60 min-w-0 divide-y'>
        <ResultRow
          runs={props.siteRuns ?? []}
          source='site'
          onSelect={props.onSelect}
        />
        <ResultRow
          runs={props.runs}
          source='user'
          onSelect={props.onSelect}
          action={
            props.allowTest !== false && (
              <Button
                variant='outline'
                size='sm'
                className='border-border/80 min-h-9 text-xs max-sm:w-full xl:min-h-7'
                onClick={props.onEstimate}
                disabled={props.disabled || props.estimating}
              >
                <Play aria-hidden className='size-3' />
                {props.estimating
                  ? t('intelligence.estimating')
                  : t('fingerprint.selfTest')}
              </Button>
            )
          }
        />
      </div>
    </article>
  )
}
