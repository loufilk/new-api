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

import { Button } from '@/components/ui/button'

import {
  getModelCapability,
  parseSizeDimensions,
} from '../lib/model-capabilities'

// Exact pixel presets, not claims that all providers support a resolution tier.
// A 4K square cannot be 4096² under GPT Image 2's 8,294,400-pixel ceiling.
const PRESETS = [
  { label: '1K', sizes: ['1024x1024', '1536x864', '864x1536'] },
  { label: '2K', sizes: ['2048x2048', '2560x1440', '1440x2560'] },
  {
    label: '4K (experimental)',
    sizes: ['2880x2880', '3840x2160', '2160x3840'],
  },
]

type Props = {
  model: string
  size: string
  sizes: string[]
  disabled: boolean
  onChange: (size: string) => void
}

export function ImageResolutionPresets(props: Props) {
  const { t } = useTranslation()
  if (!props.model.trim().toLowerCase().includes('gpt-image-2')) return null
  const capability = getModelCapability(props.model)
  const available = PRESETS.map((preset) => ({
    ...preset,
    sizes: preset.sizes.filter(
      (size) => props.sizes.includes(size) && capability.sizes.includes(size)
    ),
  })).filter((preset) => preset.sizes.length > 0)
  if (available.length === 0) return null
  const current = parseSizeDimensions(props.size)
  const ratio =
    current.width && current.height ? current.width / current.height : 1
  return (
    <div className='grid w-full min-w-0 gap-2'>
      <div
        role='group'
        aria-label={t('Output resolution')}
        className='flex flex-wrap gap-2'
      >
        {available.map((preset) => (
          <Button
            key={preset.label}
            type='button'
            variant={preset.sizes.includes(props.size) ? 'default' : 'outline'}
            size='sm'
            aria-pressed={preset.sizes.includes(props.size)}
            disabled={props.disabled}
            onClick={() => {
              const closest = preset.sizes.reduce((best, size) => {
                const a = parseSizeDimensions(best)
                const b = parseSizeDimensions(size)
                const aRatio = a.width && a.height ? a.width / a.height : 1
                const bRatio = b.width && b.height ? b.width / b.height : 1
                return Math.abs(bRatio - ratio) < Math.abs(aRatio - ratio)
                  ? size
                  : best
              })
              props.onChange(closest)
            }}
          >
            {preset.label === '4K (experimental)'
              ? t('4K (experimental)')
              : preset.label}
          </Button>
        ))}
      </div>
      <p className='text-muted-foreground text-xs'>
        {t(
          'High-resolution generation requires upstream support and may cost more. 4K uses up to 3840×2160 pixels; square output is 2880×2880. This is not upscaling.'
        )}
      </p>
    </div>
  )
}
