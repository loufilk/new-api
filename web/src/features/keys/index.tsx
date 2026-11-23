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
import { getRouteApi } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'

import { ApiKeysDialogs } from './components/api-keys-dialogs'
import { ApiKeysPrimaryButtons } from './components/api-keys-primary-buttons'
import { ApiKeysProvider, useApiKeys } from './components/api-keys-provider'
import { ApiKeysTable } from './components/api-keys-table'
import {
  consumePrivateMarketKeyDraft,
  type MarketKeyDraft,
} from './lib/market-key-draft'

const route = getRouteApi('/_authenticated/keys/')

export function ApiKeys() {
  return (
    <ApiKeysProvider>
      <ApiKeysPage />
    </ApiKeysProvider>
  )
}

function ApiKeysPage() {
  const { t } = useTranslation()
  const navigate = route.useNavigate()
  const { create, group, model } = route.useSearch()
  const { open, setOpen } = useApiKeys()
  const [draft, setDraft] = useState<MarketKeyDraft>()

  useEffect(() => {
    if (!create) return
    const privateDraft = consumePrivateMarketKeyDraft()
    if (group || model || privateDraft)
      setDraft(group || model ? { group, model } : privateDraft)
    setOpen('create')
    // One-time intent: never re-open if the user cancels or clicks Back.
    void navigate({
      replace: true,
      search: (previous) => ({
        ...previous,
        create: undefined,
        group: undefined,
        model: undefined,
      }),
    })
  }, [create, group, model, navigate, setOpen])

  useEffect(() => {
    if (open !== 'create' && !create) setDraft(undefined)
  }, [create, open])

  return (
    <>
      <SectionPageLayout fixedContent stackActionsOnMobile>
        <SectionPageLayout.Title>{t('API Keys')}</SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <ApiKeysPrimaryButtons />
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <ApiKeysTable />
        </SectionPageLayout.Content>
      </SectionPageLayout>
      <ApiKeysDialogs draft={draft} />
    </>
  )
}
