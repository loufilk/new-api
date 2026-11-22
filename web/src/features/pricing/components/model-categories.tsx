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
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'

import {
  MODEL_CATEGORIES,
  getModelCategories,
  type ModelCategory,
} from '../lib/model-categories'
import type { PricingModel } from '../types'

export function ModelCategories(props: {
  models: PricingModel[]
  value: ModelCategory
  onChange: (value: ModelCategory) => void
}) {
  const { t } = useTranslation()
  const labels = {
    text: t('Text'),
    images: t('Image Generation'),
    video: t('Video'),
    audio: t('Audio'),
    vectors: t('Vectors'),
    decision: t('Decision Models'),
    all: t('All'),
  }
  const counts = useMemo(() => {
    const result = {
      text: 0,
      images: 0,
      video: 0,
      audio: 0,
      vectors: 0,
      decision: 0,
      all: props.models.length,
    }
    for (const model of props.models) {
      for (const category of getModelCategories(model)) result[category]++
    }
    return result
  }, [props.models])
  return (
    <section
      className='mb-5 flex flex-col items-center gap-2'
      aria-label={t('Category')}
    >
      <ToggleGroup
        value={[props.value]}
        onValueChange={(values) => {
          if (values[0]) props.onChange(values[0] as ModelCategory)
        }}
        spacing={2}
        variant='outline'
        className='max-w-full flex-wrap justify-center'
        aria-label={t('Category')}
      >
        {MODEL_CATEGORIES.map((category) => (
          <ToggleGroupItem
            key={category}
            value={category}
            className='h-10 gap-2 px-4 text-base'
          >
            {labels[category]}{' '}
            <span className='text-muted-foreground text-sm tabular-nums'>
              {counts[category]}
            </span>
          </ToggleGroupItem>
        ))}
      </ToggleGroup>
      {props.value === 'vectors' && (
        <p className='text-muted-foreground text-sm'>
          {t('Embeddings and reranking')}
        </p>
      )}
    </section>
  )
}
