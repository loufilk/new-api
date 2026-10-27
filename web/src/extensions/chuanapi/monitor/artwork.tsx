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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'

import { artworkURL } from './api'
import type { MonitorResult } from './types'

export function Artwork(props: { result: MonitorResult; thumbnail?: boolean }) {
  const { t } = useTranslation()
  const [failed, setFailed] = useState(false)
  if (failed) {
    return (
      <EmptyState
        title={t('monitor.artworkUnavailable')}
        className='min-h-40'
      />
    )
  }
  return (
    <img
      src={artworkURL(props.result.id, props.result.revision, props.thumbnail)}
      alt={t('monitor.artworkAlt')}
      loading='lazy'
      referrerPolicy='no-referrer'
      onError={() => setFailed(true)}
      className='bg-muted/30 aspect-[4/3] w-full rounded-lg object-contain'
    />
  )
}
