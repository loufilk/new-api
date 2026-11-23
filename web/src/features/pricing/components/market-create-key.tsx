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
import { Link, useNavigate } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { stagePrivateMarketKeyDraft } from '@/features/keys/lib/market-key-draft'

import { EXCLUDED_GROUPS, FILTER_ALL } from '../constants'
import type { PricingModel } from '../types'

export function MarketCreateKey(props: {
  model: PricingModel
  selectedGroup?: string
}) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const groups = props.model.enable_groups.filter(
    (group) => !EXCLUDED_GROUPS.includes(group)
  )
  let group = groups.length === 1 ? groups[0] : undefined
  if (
    props.selectedGroup &&
    props.selectedGroup !== FILTER_ALL &&
    groups.includes(props.selectedGroup)
  ) {
    group = props.selectedGroup
  }
  if (props.model.group_keys_are_display_only) group = undefined
  const name = props.model.model_name
  // Names of exclusive groups and of their models must not appear in
  // redirect URLs, copied links or browser history. Draft is single-use.
  if (group && props.model.group_catalog?.[group]?.exclusive) {
    const privateGroup = group
    return (
      <Button
        size='sm'
        variant='outline'
        type='button'
        onClick={() => {
          stagePrivateMarketKeyDraft(privateGroup, name)
          void navigate({ to: '/keys', search: { create: true } })
        }}
      >
        {t('Create API Key')}
      </Button>
    )
  }
  const sensitiveModel = groups.some(
    (key) => props.model.group_catalog?.[key]?.exclusive
  )
  return (
    <Button
      role='link'
      variant='outline'
      size='sm'
      render={
        <Link
          to='/keys'
          search={{
            create: true,
            group,
            model: sensitiveModel ? undefined : name,
          }}
        />
      }
    >
      {t('Create API Key')}
    </Button>
  )
}
