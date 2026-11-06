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

import { Badge } from '@/components/ui/badge'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { ModelTestCard } from './model-card'
import type {
  TestCatalogue,
  TestHistory,
  TestRun,
  TestSelection,
} from './types'

type ModelSelection = Pick<TestSelection, 'group' | 'model'>
type ModelGroup = { group: string; models: string[] }

function groupModels(selections: ModelSelection[]): ModelGroup[] {
  const groups = new Map<string, Set<string>>()
  for (const selection of selections) {
    const models = groups.get(selection.group) ?? new Set<string>()
    models.add(selection.model)
    groups.set(selection.group, models)
  }
  return [...groups.entries()]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([group, models]) => ({ group, models: [...models].sort() }))
}

export function ModelTestGroups(props: {
  targets: ModelSelection[]
  catalogue?: TestCatalogue
  eligibleModels?: TestHistory['eligible_models_by_group']
  runs: TestRun[]
  siteRuns: TestRun[]
  disabled: boolean
  estimateSelection?: TestSelection
  onEstimate: (selection: ModelSelection) => void
  onSelect: (run: TestRun) => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  return groupModels(props.targets).map(({ group, models }) => {
    const groupInfo = props.catalogue?.groups.find(
      (item) => item.name === group
    )
    return (
      <section
        key={group}
        className='bg-card border-border/70 min-w-0 overflow-hidden rounded-xl border'
        aria-label={group}
      >
        <header className='flex flex-wrap items-center gap-2.5 px-4 py-2.5'>
          <h2 className='text-sm font-semibold break-all'>{group}</h2>
          {groupInfo && (
            <Badge
              variant='secondary'
              className='rounded-full bg-sky-50 px-2 py-0.5 text-[11px] font-medium text-sky-700 dark:bg-sky-950 dark:text-sky-300'
            >
              {t('Ratio')} {formatNumber(groupInfo.ratio, locale)}×
            </Badge>
          )}
        </header>
        {models.map((model) => (
          <ModelTestCard
            key={model}
            model={model}
            group={group}
            runs={props.runs.filter(
              (run) => run.group === group && run.model === model
            )}
            siteRuns={props.siteRuns.filter(
              (run) => run.group === group && run.model === model
            )}
            allowTest={props.eligibleModels?.[group]?.includes(model) === true}
            disabled={props.disabled}
            estimating={
              props.estimateSelection?.model === model &&
              props.estimateSelection?.group === group
            }
            onEstimate={() => props.onEstimate({ model, group })}
            onSelect={props.onSelect}
          />
        ))}
      </section>
    )
  })
}
