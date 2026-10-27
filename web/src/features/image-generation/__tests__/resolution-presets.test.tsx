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
import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'

import { ImageResolutionPresets } from '../components/ImageResolutionPresets'
import { ImageResolutionPreview } from '../components/ImageResolutionPreview'
import {
  getModelCapability,
  resolveSettingsForModel,
} from '../lib/model-capabilities'
import {
  buildEditsFormData,
  buildGenerationsBody,
} from '../lib/request-builders'

test('2K and 4K preserve orientation and emit concrete sizes without rewriting the model', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  const props = {
    model: 'gpt-image-2',
    size: '864x1536',
    sizes: getModelCapability('gpt-image-2').sizes,
    disabled: false,
    onChange,
  }
  const { rerender } = render(<ImageResolutionPresets {...props} />)
  await user.click(screen.getByRole('button', { name: '2K' }))
  expect(onChange).toHaveBeenLastCalledWith('1440x2560')
  rerender(<ImageResolutionPresets {...props} size='1440x2560' />)
  expect(screen.getByRole('button', { name: '2K' })).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  await user.click(screen.getByRole('button', { name: '4K (experimental)' }))
  expect(onChange).toHaveBeenLastCalledWith('2160x3840')
  const params = {
    model: 'gpt-image-2',
    prompt: 'test',
    size: '2160x3840',
    quality: 'high',
    style: '',
    count: 1,
  }
  expect(buildGenerationsBody(params)).toEqual({
    model: 'gpt-image-2',
    prompt: 'test',
    size: '2160x3840',
    quality: 'high',
    n: 1,
  })
  expect(
    buildEditsFormData({
      ...params,
      images: [new File(['x'], 'reference.png', { type: 'image/png' })],
    }).get('size')
  ).toBe('2160x3840')
  expect(resolveSettingsForModel('gpt-image-2', params).size).toBe('2160x3840')
  expect(resolveSettingsForModel('dall-e-3', params).size).toBe('1024x1024')
})

test('square 4K respects the model pixel ceiling and generation disables changes', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  const props = {
    model: 'gpt-image-2',
    size: '2048x2048',
    sizes: getModelCapability('gpt-image-2').sizes,
    disabled: false,
    onChange,
  }
  const { rerender } = render(<ImageResolutionPresets {...props} />)
  const button = screen.getByRole('button', { name: '4K (experimental)' })
  button.focus()
  await user.keyboard('{Enter}')
  expect(onChange).toHaveBeenLastCalledWith('2880x2880')
  rerender(<ImageResolutionPresets {...props} disabled />)
  expect(button).toBeDisabled()
})

test('fixed-resolution aliases and unrelated providers do not gain unsupported tiers', () => {
  const { rerender } = render(
    <ImageResolutionPresets
      model='gpt-image-2-2k'
      size='auto'
      sizes={getModelCapability('gpt-image-2-2k').sizes}
      disabled={false}
      onChange={vi.fn()}
    />
  )
  expect(screen.getByRole('button', { name: '2K' })).toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: '4K (experimental)' })
  ).not.toBeInTheDocument()
  rerender(
    <ImageResolutionPresets
      model='grok-imagine-image-2.0-console'
      size='auto'
      sizes={['auto', '1:1']}
      disabled={false}
      onChange={vi.fn()}
    />
  )
  expect(
    screen.queryByRole('group', { name: 'Output resolution' })
  ).not.toBeInTheDocument()
})

test('actual size stays unverified until image decode and reports downgraded output without changing the original', () => {
  const { rerender } = render(
    <ImageResolutionPreview
      url='https://images.example/a.png'
      prompt='image'
      requestedSize='3840x2160'
    />
  )
  expect(screen.getByText(/Not yet verified/)).toBeInTheDocument()
  const image = screen.getByAltText('image')
  Object.defineProperties(image, {
    naturalWidth: { value: 1024, configurable: true },
    naturalHeight: { value: 1024, configurable: true },
  })
  fireEvent.load(image)
  expect(screen.getByText(/1024×1024/)).toBeInTheDocument()
  expect(screen.getByRole('status')).toHaveTextContent(
    'The returned image size differs from the requested size.'
  )
  expect(image).toHaveAttribute('src', 'https://images.example/a.png')
  rerender(
    <ImageResolutionPreview
      url='https://images.example/b.png'
      prompt='image'
      requestedSize='auto'
    />
  )
  expect(screen.getByText(/Not yet verified/)).toBeInTheDocument()
  expect(screen.queryByRole('status')).not.toBeInTheDocument()
  fireEvent.error(screen.getByAltText('image'))
  expect(screen.getByText(/Not yet verified/)).toBeInTheDocument()
})
