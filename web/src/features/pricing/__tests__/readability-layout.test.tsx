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
import { render, screen, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { ModelCard } from '../components/model-card'
import { ModelCategories } from '../components/model-categories'
import type { PricingModel } from '../types'

const model: PricingModel = {
  id: 1,
  model_name:
    'provider/long-model-name-with-version-and-context-window-20261024',
  description: 'A readable model description.',
  quota_type: 0,
  model_ratio: 1,
  completion_ratio: 3,
  enable_groups: ['default'],
  output_modalities: ['text'],
}

describe('pricing readability layout', () => {
  it('keeps all six category controls large and able to wrap on narrow screens', () => {
    render(<ModelCategories models={[model]} value='all' onChange={vi.fn()} />)
    const categories = screen.getAllByRole('button')
    expect(categories).toHaveLength(6)
    for (const category of categories) {
      expect(category).toHaveClass('h-10', 'px-4', 'text-base')
      expect(within(category).getByText(/^[01]$/)).toHaveClass('text-sm')
    }
    expect(categories[0].parentElement).toHaveClass('flex-wrap', 'max-w-full')
  })

  it('shows the complete long model name at the larger size without a line clamp', () => {
    render(<ModelCard model={model} onClick={vi.fn()} />)
    const heading = screen.getByRole('heading', { name: model.model_name })
    expect(heading).toHaveClass('text-lg', '[overflow-wrap:anywhere]')
    expect(heading).not.toHaveClass('line-clamp-2', 'truncate')
    expect(heading).toHaveAttribute('title', model.model_name)
  })

  it('gives the description and card content the approved larger spacing', () => {
    render(<ModelCard model={model} onClick={vi.fn()} />)
    const description = screen.getByText('A readable model description.')
    expect(description).toHaveClass('text-[15px]', 'leading-6')
    expect(description.closest('[data-slot="card-content"]')).toHaveClass(
      'gap-4',
      'px-5'
    )
    expect(description.closest('[data-slot="card"]')).toHaveClass(
      'gap-4',
      'pt-5'
    )
  })

  it('keeps larger price amounts and their units wrappable instead of clipping', () => {
    render(<ModelCard model={model} onClick={vi.fn()} tokenUnit='M' />)
    const pricing = screen.getByRole('group', { name: 'Pricing' })
    for (const unit of within(pricing).getAllByText('/ 1M')) {
      expect(unit).toHaveClass(
        'text-sm',
        'whitespace-normal',
        '[overflow-wrap:anywhere]'
      )
      expect(unit.parentElement).toHaveClass('text-lg', 'flex-wrap')
    }
  })

  it('keeps a dynamic price range and a long custom unit wrappable at the larger size', () => {
    render(
      <ModelCard
        model={{
          ...model,
          billing_mode: 'tiered_expr',
          billing_expr:
            'u("mode") == "pro" ? tier("pro", u("count") * 987654321.12) : tier("std", u("count") * 123456789.12)',
          billing_usage_schema: {
            count: {
              type: 'number',
              unit: 'count',
              unitLabel: { en: 'custom-billable-output-with-a-long-unit-name' },
            },
            mode: { enum: ['std', 'pro'] },
          },
        }}
        onClick={vi.fn()}
      />
    )
    const unit = screen.getByText(
      '/ custom-billable-output-with-a-long-unit-name'
    )
    expect(unit).toHaveClass(
      'max-w-full',
      'whitespace-normal',
      '[overflow-wrap:anywhere]'
    )
    expect(unit.parentElement).toHaveClass(
      'text-lg',
      'flex-wrap',
      '[overflow-wrap:anywhere]'
    )
    expect(unit.parentElement).toHaveTextContent(' – ')
  })

  it('allows performance details to wrap rather than overflowing a narrow card', () => {
    render(<ModelCard model={model} onClick={vi.fn()} />)
    expect(
      screen.getByLabelText('Performance metrics for the last 24 hours')
    ).toHaveClass('flex-wrap')
    expect(screen.getByRole('button', { name: 'Details' })).toHaveClass(
      'h-9',
      'text-sm'
    )
  })
})
