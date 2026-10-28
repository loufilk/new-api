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
import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, test, vi } from 'vitest'

import { HistoryPanel } from '../components/HistoryPanel'
import type { ImageHistoryEntry } from '../types'

type PanelProps = Parameters<typeof HistoryPanel>[0]

function renderPanel(overrides: Partial<PanelProps> = {}) {
  const props: PanelProps = {
    entries: [],
    totalCount: 0,
    query: '',
    onQueryChange: vi.fn(),
    orientation: 'all',
    onOrientationChange: vi.fn(),
    onRemove: vi.fn(),
    onClear: vi.fn(),
    onKeepLocally: vi.fn(async () => undefined),
    resolveUrl: (_id, fallback) => fallback,
    ...overrides,
  }
  render(<HistoryPanel {...props} />)
  return props
}

function makeEntry(id: string): ImageHistoryEntry {
  const now = Date.now()
  return {
    id,
    prompt: `prompt ${id}`,
    model: 'dall-e-3',
    settings: {
      size: '1024x1024',
      quality: 'standard',
      style: 'vivid',
      count: 1,
    },
    createdAt: now,
    results: [
      {
        id: `${id}-r`,
        type: 'url',
        source: 'https://cdn.example/a.png',
        keptLocally: false,
        createdAt: now,
      },
    ],
  }
}

describe('history panel separates empty history from empty filters', () => {
  test('results toolbar wraps on small screens and keeps filters compact on desktop', () => {
    renderPanel()
    const heading = screen.getByRole('heading', { name: 'Generation results' })
    expect(heading.parentElement).toHaveClass('flex-wrap')
    expect(screen.getByLabelText('Search history').parentElement).toHaveClass(
      'sm:w-64'
    )
    expect(
      screen.getByText('No history yet.').closest('[data-slot="empty"]')
    ).toHaveClass('border-dashed')
  })
  test('shows the first-run hint only when no history exists at all', () => {
    renderPanel({ entries: [], totalCount: 0 })

    expect(screen.getByText('No history yet.')).toBeInTheDocument()
    expect(screen.queryByText('No matching history.')).not.toBeInTheDocument()
  })

  test('shows a no-match hint when filters hide every existing entry', () => {
    renderPanel({ entries: [], totalCount: 3 })

    expect(screen.queryByText('No history yet.')).not.toBeInTheDocument()
    expect(screen.getByText('No matching history.')).toBeInTheDocument()
  })

  test('shows neither hint once entries are visible', () => {
    renderPanel({ entries: [makeEntry('e1')], totalCount: 1 })

    expect(screen.queryByText('No history yet.')).not.toBeInTheDocument()
    expect(screen.queryByText('No matching history.')).not.toBeInTheDocument()
    expect(screen.getByText('prompt e1')).toBeInTheDocument()
  })
})

test('keeps previous artwork visible alongside one placeholder per requested image', () => {
  renderPanel({
    entries: [makeEntry('old')],
    totalCount: 1,
    isGenerating: true,
    pendingCount: 3,
  })
  expect(screen.getAllByRole('status')).toHaveLength(3)
  expect(screen.getByAltText('prompt old')).toBeInTheDocument()
})
test('requires confirmation to clear all history and cancellation preserves it', async () => {
  const props = renderPanel({ entries: [makeEntry('old')], totalCount: 1 })
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Clear history' }))
  expect(props.onClear).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(props.onClear).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Clear history' }))
  await user.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Delete',
    })
  )
  expect(props.onClear).toHaveBeenCalledTimes(1)
})
test('previews complete prompt and returns the complete batch for reuse', async () => {
  const entry = makeEntry('old')
  const onReuse = vi.fn()
  renderPanel({ entries: [entry], totalCount: 1, onReuse })
  const user = userEvent.setup()
  await user.click(
    screen.getByRole('button', { name: 'Preview image for prompt old' })
  )
  const dialog = screen.getByRole('dialog')
  expect(within(dialog).getByText(entry.prompt)).toBeInTheDocument()
  await user.click(within(dialog).getByRole('button', { name: 'Reuse' }))
  expect(onReuse).toHaveBeenCalledWith(entry)
})
test('deleting one image confirms removal of its entire batch', async () => {
  const props = renderPanel({ entries: [makeEntry('old')], totalCount: 1 })
  const user = userEvent.setup()
  await user.click(
    screen.getByRole('button', { name: 'Delete history prompt old' })
  )
  expect(props.onRemove).not.toHaveBeenCalled()
  await user.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Delete',
    })
  )
  expect(props.onRemove).toHaveBeenCalledWith('old')
})

test('records decoded pixel dimensions without stretching to the requested size', () => {
  const onDimensions = vi.fn()
  renderPanel({ entries: [makeEntry('geometry')], totalCount: 1, onDimensions })
  const image = screen.getByAltText('prompt geometry')
  Object.defineProperties(image, {
    naturalWidth: { configurable: true, value: 768 },
    naturalHeight: { configurable: true, value: 1344 },
  })
  fireEvent.load(image)
  expect(onDimensions).toHaveBeenCalledWith(
    'geometry',
    'geometry-r',
    'https://cdn.example/a.png',
    768,
    1344
  )
  expect(image).toHaveClass('object-contain')
  expect(image).not.toHaveAttribute('width')
  expect(image).not.toHaveAttribute('height')
})

test('a failed or undecodable thumbnail stays unverified', () => {
  const onDimensions = vi.fn()
  renderPanel({ entries: [makeEntry('broken')], totalCount: 1, onDimensions })
  const image = screen.getByAltText('prompt broken')
  fireEvent.load(image)
  fireEvent.error(image)
  expect(onDimensions).not.toHaveBeenCalled()
  expect(
    screen.getByText(/Actual image size.*Not yet verified/)
  ).toBeInTheDocument()
})
